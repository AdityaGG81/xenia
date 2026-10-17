// Package service contains Xenia node-domain state and its initial HTTP adapter.
package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
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
	peers       map[string]time.Time
	http        *http.Client
	messageNo   uint64
	storageDir  string
	storageFile *os.File
	listenAddr  string
}

type Option func(*Server)

// WithListenAddr configures the HTTP listen address (e.g. "0.0.0.0:8080") so we can broadcast our API url.
func WithListenAddr(addr string) Option {
	return func(s *Server) { s.listenAddr = addr }
}

// WithStorageDir configures the directory for offline message persistence.
func WithStorageDir(dir string) Option {
	return func(s *Server) { s.storageDir = dir }
}

func New(nodeID string, options ...Option) *Server {
	if strings.TrimSpace(nodeID) == "" {
		nodeID = "unnamed-node"
	}
	s := &Server{
		nodeID:   nodeID,
		presence: make(map[string]Presence),
		calls:    make(map[string]Call),
		peers:    make(map[string]time.Time),
		http:     &http.Client{Timeout: 3 * time.Second},
	}
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

// StartUDPDiscovery starts a UDP broadcaster and listener on port 9999.
func (s *Server) StartUDPDiscovery() {
	go s.listenUDP()
	go s.broadcastUDP()
}

type DiscoveryPacket struct {
	NodeID string `json:"node_id"`
	APIURL string `json:"api_url"`
}

func (s *Server) listenUDP() {
	addr, err := net.ResolveUDPAddr("udp4", ":9999")
	if err != nil {
		log.Printf("udp resolve failed: %v", err)
		return
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Printf("udp listen failed: %v", err)
		return
	}
	defer conn.Close()

	buf := make([]byte, 1024)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err == nil {
			var packet DiscoveryPacket
			if err := json.Unmarshal(buf[:n], &packet); err == nil {
				if packet.NodeID != s.nodeID && packet.APIURL != "" {
					s.mu.Lock()
					s.peers[packet.APIURL] = time.Now()
					s.mu.Unlock()
				}
			}
		}
	}
}

func (s *Server) broadcastUDP() {
	apiURL := "http://" + s.listenAddr
	if strings.HasPrefix(s.listenAddr, ":") {
		apiURL = "http://localhost" + s.listenAddr
	} else if strings.HasPrefix(s.listenAddr, "0.0.0.0:") {
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						apiURL = "http://" + ipnet.IP.String() + ":" + strings.Split(s.listenAddr, ":")[1]
						break
					}
				}
			}
		}
	}

	packet := DiscoveryPacket{NodeID: s.nodeID, APIURL: apiURL}
	b, _ := json.Marshal(packet)

	for {
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					ip := ipnet.IP.To4()
					if ip != nil {
						mask := ipnet.Mask
						bcast := make(net.IP, 4)
						for i := 0; i < 4; i++ {
							bcast[i] = ip[i] | ^mask[i]
						}
						addr, _ := net.ResolveUDPAddr("udp4", bcast.String()+":9999")
						if conn, err := net.DialUDP("udp4", nil, addr); err == nil {
							conn.Write(b)
							conn.Close()
						}
					}
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
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
	now := time.Now().UTC()
	s.mu.Lock()
	s.messageNo++
	call := Call{ID: fmt.Sprintf("%s-call-%d", s.nodeID, s.messageNo), From: request.From, To: request.To, State: "ringing", CreatedAt: now, UpdatedAt: now}
	s.calls[call.ID] = call
	s.mu.Unlock()
	
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()

	for _, peer := range peers {
		if err := s.sendJSON(http.MethodPost, peer+"/internal/calls", call); err != nil {
			log.Printf("could not relay call to %s: %v", peer, err)
		}
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
	
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()
	
	for _, peer := range peers {
		if err := s.sendJSON(http.MethodPost, peer+"/internal/calls/"+url.PathEscape(id)+"/state", map[string]string{"state": call.State}); err != nil {
			log.Printf("could not relay call state to %s: %v", peer, err)
		}
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

// activePeers returns a slice of currently active peers (seen in last 30s)
func (s *Server) activePeers() []string {
	var peers []string
	now := time.Now()
	for url, lastSeen := range s.peers {
		if now.Sub(lastSeen) < 30*time.Second {
			peers = append(peers, url)
		}
	}
	return peers
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
	peers := s.activePeers()
	s.mu.Unlock()

	go func() {
		for _, peer := range peers {
			if err := s.sendJSON("POST", peer+"/internal/presence", presence); err != nil {
				log.Printf("node %s: failed to forward presence to %s: %v", s.nodeID, peer, err)
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

	// Forward to peer nodes for delivery
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()

	go func() {
		forwarded := false
		for _, peer := range peers {
			if err := s.sendJSON(http.MethodPost, peer+"/internal/message", message); err == nil {
				forwarded = true
			}
		}
		
		if forwarded {
			s.mu.Lock()
			for i := range s.messages {
				if s.messages[i].ID == message.ID {
					s.messages[i].State = "forwarded"
					s.appendMessageLog(s.messages[i])
					break
				}
			}
			s.mu.Unlock()
		}
	}()

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
