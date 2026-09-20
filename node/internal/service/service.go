// Package service contains Xenia node-domain state and its initial HTTP adapter.
package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Presence struct {
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	State     string    `json:"state"`
}

// Call is signalling state only; it never contains audio or media data.
type Call struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Server struct {
	nodeID      string
	mu          sync.RWMutex
	presence    map[string]Presence
	messages    []Message
	calls       map[string]Call
	peerURL     string
	http        *http.Client
	messageNo   uint64
	storageDir  string
	storageFile *os.File
}

type Option func(*Server)

// WithPeerURL configures the one explicit signalling peer used in this milestone.
func WithPeerURL(peerURL string) Option {
	return func(s *Server) { s.peerURL = strings.TrimRight(peerURL, "/") }
}

// WithStorageDir configures the directory for offline message persistence.
func WithStorageDir(dir string) Option {
	return func(s *Server) { s.storageDir = dir }
}

func New(nodeID string, options ...Option) *Server {
	if strings.TrimSpace(nodeID) == "" {
		nodeID = "unnamed-node"
	}
	s := &Server{nodeID: nodeID, presence: make(map[string]Presence), calls: make(map[string]Call), http: &http.Client{Timeout: 3 * time.Second}}
	for _, option := range options {
		option(s)
	}

	if s.storageDir != "" {
		_ = os.MkdirAll(s.storageDir, 0755)
		path := filepath.Join(s.storageDir, "messages.jsonl")
		f, err := os.Open(path)
		if err == nil {
			scanner := bufio.NewScanner(f)
			msgMap := make(map[string]Message)
			for scanner.Scan() {
				var m Message
				if err := json.Unmarshal(scanner.Bytes(), &m); err == nil {
					msgMap[m.ID] = m
					// Recover sequence number from local messages
					if strings.HasPrefix(m.ID, s.nodeID+"-") {
						if num, err := strconv.ParseUint(strings.TrimPrefix(m.ID, s.nodeID+"-"), 10, 64); err == nil {
							if num > s.messageNo {
								s.messageNo = num
							}
						}
					}
				}
			}
			f.Close()
			for _, m := range msgMap {
				s.messages = append(s.messages, m)
			}
			sort.Slice(s.messages, func(i, j int) bool {
				return s.messages[i].CreatedAt.Before(s.messages[j].CreatedAt)
			})
		}
		// Open for appending new/updated messages
		s.storageFile, _ = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	}

	return s
}

// appendMessageLog must be called with s.mu held.
func (s *Server) appendMessageLog(m Message) {
	if s.storageFile == nil {
		return
	}
	b, err := json.Marshal(m)
	if err == nil {
		b = append(b, '\n')
		_, _ = s.storageFile.Write(b)
		_ = s.storageFile.Sync()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /users", s.users)
	mux.HandleFunc("POST /presence", s.setPresence)
	mux.HandleFunc("POST /message", s.bufferMessage)
	mux.HandleFunc("GET /messages/{user}", s.getMessages)
	mux.HandleFunc("POST /calls", s.createCall)
	mux.HandleFunc("GET /calls/{id}", s.getCall)
	mux.HandleFunc("GET /calls", s.listCalls)
	mux.HandleFunc("PATCH /calls/{id}", s.answerCall)
	mux.HandleFunc("POST /internal/calls", s.receiveCall)
	mux.HandleFunc("POST /internal/calls/{id}/state", s.receiveCallState)
	mux.HandleFunc("POST /internal/message", s.receiveMessage)
	mux.HandleFunc("POST /internal/presence", s.receivePresence)
	return mux
}

func (s *Server) createCall(w http.ResponseWriter, r *http.Request) {
	var request struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.From) == "" || strings.TrimSpace(request.To) == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if s.peerURL == "" {
		writeError(w, http.StatusServiceUnavailable, "no signalling peer configured")
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	s.messageNo++
	call := Call{ID: fmt.Sprintf("%s-call-%d", s.nodeID, s.messageNo), From: request.From, To: request.To, State: "ringing", CreatedAt: now, UpdatedAt: now}
	s.calls[call.ID] = call
	s.mu.Unlock()
	if err := s.sendJSON(http.MethodPost, s.peerURL+"/internal/calls", call); err != nil {
		writeError(w, http.StatusBadGateway, "could not relay call: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, call)
}

func (s *Server) receiveCall(w http.ResponseWriter, r *http.Request) {
	var call Call
	if !decodeJSON(w, r, &call) {
		return
	}
	if call.ID == "" || call.From == "" || call.To == "" {
		writeError(w, http.StatusBadRequest, "call id, from, and to are required")
		return
	}
	call.State = "ringing"
	call.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	s.calls[call.ID] = call
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, call)
}

func (s *Server) getCall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	call, ok := s.calls[id]
	s.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, "call not found")
		return
	}
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) listCalls(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	calls := make(map[string]Call)
	for id, call := range s.calls {
		calls[id] = call
	}
	writeJSON(w, http.StatusOK, map[string]any{"calls": calls})
}

func (s *Server) answerCall(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Accepted bool `json:"accepted"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	call, ok := s.calls[id]
	if ok {
		if request.Accepted {
			call.State = "accepted"
		} else {
			call.State = "rejected"
		}
		call.UpdatedAt = time.Now().UTC()
		s.calls[id] = call
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "call not found")
		return
	}
	if s.peerURL == "" {
		writeError(w, http.StatusServiceUnavailable, "no signalling peer configured")
		return
	}
	if err := s.sendJSON(http.MethodPost, s.peerURL+"/internal/calls/"+url.PathEscape(id)+"/state", map[string]string{"state": call.State}); err != nil {
		writeError(w, http.StatusBadGateway, "could not relay call state: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) receiveCallState(w http.ResponseWriter, r *http.Request) {
	var request struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.State != "accepted" && request.State != "rejected" {
		writeError(w, http.StatusBadRequest, "invalid call state")
		return
	}
	s.mu.Lock()
	call, ok := s.calls[r.PathValue("id")]
	if ok {
		call.State = request.State
		call.UpdatedAt = time.Now().UTC()
		s.calls[call.ID] = call
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "call not found")
		return
	}
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) sendJSON(method, endpoint string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("peer returned %s", response.Status)
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"node_id": s.nodeID, "status": "ok"})
}

func (s *Server) users(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	users := make([]Presence, 0, len(s.presence))
	for _, presence := range s.presence {
		users = append(users, presence)
	}
	s.mu.RUnlock()
	sort.Slice(users, func(i, j int) bool { return users[i].UserID < users[j].UserID })
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) setPresence(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID string `json:"user_id"`
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.Status) == "" {
		writeError(w, http.StatusBadRequest, "user_id and status are required")
		return
	}

	presence := Presence{UserID: request.UserID, Status: request.Status, UpdatedAt: time.Now().UTC()}
	s.mu.Lock()
	s.presence[presence.UserID] = presence
	s.mu.Unlock()

	go func() {
		if s.peerURL != "" {
			if err := s.sendJSON("POST", s.peerURL+"/internal/presence", presence); err != nil {
				log.Printf("node %s: failed to forward presence to peer: %v", s.nodeID, err)
			}
		}
	}()

	writeJSON(w, http.StatusOK, presence)
}

func (s *Server) receivePresence(w http.ResponseWriter, r *http.Request) {
	var presence Presence
	if !decodeJSON(w, r, &presence) {
		return
	}
	s.mu.Lock()
	s.presence[presence.UserID] = presence
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (s *Server) bufferMessage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		From string `json:"from"`
		To   string `json:"to"`
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.From) == "" || strings.TrimSpace(request.To) == "" || strings.TrimSpace(request.Body) == "" {
		writeError(w, http.StatusBadRequest, "from, to, and body are required")
		return
	}

	s.mu.Lock()
	s.messageNo++
	message := Message{
		ID:        fmt.Sprintf("%s-%d", s.nodeID, s.messageNo),
		From:      request.From,
		To:        request.To,
		Body:      request.Body,
		CreatedAt: time.Now().UTC(),
		State:     "buffered",
	}
	s.messages = append(s.messages, message)
	s.appendMessageLog(message)
	s.mu.Unlock()

	// Forward to peer node for delivery if a peer is configured.
	if s.peerURL != "" {
		if err := s.sendJSON(http.MethodPost, s.peerURL+"/internal/message", message); err != nil {
			// Buffered locally but forwarding failed; the message can be retried later.
			message.State = "buffered"
			writeJSON(w, http.StatusAccepted, message)
			return
		}
		s.mu.Lock()
		for i := range s.messages {
			if s.messages[i].ID == message.ID {
				s.messages[i].State = "forwarded"
				s.appendMessageLog(s.messages[i])
				break
			}
		}
		s.mu.Unlock()
		message.State = "forwarded"
	}

	writeJSON(w, http.StatusAccepted, message)
}

func (s *Server) receiveMessage(w http.ResponseWriter, r *http.Request) {
	var incoming Message
	if !decodeJSON(w, r, &incoming) {
		return
	}
	if incoming.ID == "" || incoming.From == "" || incoming.To == "" {
		writeError(w, http.StatusBadRequest, "id, from, and to are required")
		return
	}
	incoming.State = "delivered"
	incoming.CreatedAt = incoming.CreatedAt.UTC()
	s.mu.Lock()
	s.messages = append(s.messages, incoming)
	s.appendMessageLog(incoming)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, incoming)
}

func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	if strings.TrimSpace(user) == "" {
		writeError(w, http.StatusBadRequest, "user is required")
		return
	}
	s.mu.RLock()
	result := make([]Message, 0)
	for _, msg := range s.messages {
		if msg.To == user || msg.From == user {
			result = append(result, msg)
		}
	}
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"messages": result})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
