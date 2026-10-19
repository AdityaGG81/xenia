package service

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		return err == nil && strings.EqualFold(parsed.Host, r.Host)
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// Handler returns the HTTP API handler. Static assets are mounted by the command.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public REST endpoints
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /users", s.handleUsers)
	mux.HandleFunc("POST /register", s.handleRegister)

	// WebSocket endpoint
	mux.HandleFunc("GET /ws", s.handleWebSocket)

	// Internal node-to-node endpoints
	mux.Handle("POST /internal/sync", s.requireMeshToken(http.HandlerFunc(s.handleInternalSync)))
	mux.Handle("POST /internal/message", s.requireMeshToken(http.HandlerFunc(s.handleInternalMessage)))
	mux.Handle("POST /internal/message-ack", s.requireMeshToken(http.HandlerFunc(s.handleInternalMessageAck)))
	mux.Handle("POST /internal/user-event", s.requireMeshToken(http.HandlerFunc(s.handleInternalUserEvent)))
	mux.Handle("POST /internal/call-signal", s.requireMeshToken(http.HandlerFunc(s.handleInternalCallSignal)))

	return mux
}

func (s *Server) handleInternalMessageAck(w http.ResponseWriter, r *http.Request) {
	var ack MessageAck
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&ack); err != nil || ack.MessageID == "" || ack.OriginNode == "" {
		writeError(w, http.StatusBadRequest, "message_id and origin_node are required")
		return
	}
	s.mu.Lock()
	if s.messageAcks[ack.MessageID] {
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	s.messageAcks[ack.MessageID] = true
	var message *Message
	for i := range s.messages {
		if s.messages[i].ID == ack.MessageID {
			if s.messages[i].OriginNode != "" && s.messages[i].OriginNode != ack.OriginNode {
				delete(s.messageAcks, ack.MessageID)
				s.mu.Unlock()
				writeError(w, http.StatusConflict, "message origin mismatch")
				return
			}
			s.messages[i].State = "delivered"
			copy := s.messages[i]
			message = &copy
			if err := s.persistMessage(copy); err != nil {
				log.Printf("[%s] persist ack %s: %v", s.nodeID, ack.MessageID, err)
			}
			break
		}
	}
	if message == nil {
		delete(s.messageAcks, ack.MessageID)
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	s.mu.Unlock()
	if message != nil {
		if sender := s.localClient(message.From); sender != nil {
			sender.sendJSON(WSMessage{Type: "message_status", Message: message})
		}
	}
	if ack.OriginNode != s.nodeID && ack.Hops < maxMeshHops {
		go s.forwardMessageAck(ack)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (s *Server) requireMeshToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.meshToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Xenia-Mesh-Token")), []byte(s.meshToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid mesh token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username string `json:"username"`
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid registration request")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	if !validUsername(request.Username) {
		writeError(w, http.StatusBadRequest, "username must be 1–32 letters, numbers, dots, dashes, or underscores")
		return
	}
	deviceID, err := s.registerDevice(request.Username, strings.TrimSpace(request.DeviceID))
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"device_id": deviceID, "username": request.Username})
}

func validUsername(username string) bool {
	if len(username) == 0 || utf8.RuneCountInString(username) > 32 {
		return false
	}
	for _, r := range username {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// ──────────────────────────────────────────────
//  Public HTTP Handlers
// ──────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	peers := s.activePeers()
	peerCount := len(peers)
	clientCount := len(s.clients)
	storageState := "in-memory"
	if s.storageDir != "" {
		storageState = "ok"
		if s.storageFile == nil || s.storageErr != "" {
			storageState = "degraded"
		}
	}
	s.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":    s.nodeID,
		"status":     "ok",
		"peers":      peerCount,
		"peer_nodes": peers,
		"clients":    clientCount,
		"storage":    storageState,
	})
}

func (s *Server) handleUsers(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	users := s.collectOnlineUsers()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// ──────────────────────────────────────────────
//  WebSocket Handler
// ──────────────────────────────────────────────

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if deviceID == "" {
		http.Error(w, "device_id query parameter required", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	username := ""
	for name, id := range s.deviceIDs {
		if id == deviceID {
			username = name
			break
		}
	}
	s.mu.RUnlock()
	if username == "" {
		http.Error(w, "unknown device_id; register first", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[%s] ws upgrade failed: %v", s.nodeID, err)
		return
	}

	client := &Client{
		server:   s,
		conn:     conn,
		deviceID: deviceID,
		username: username,
		send:     make(chan []byte, 256),
		done:     make(chan struct{}),
	}

	s.registerClient(client)
	go client.writePump()
	client.readPump() // blocks until disconnect
}

// ──────────────────────────────────────────────
//  Internal Node-to-Node Handlers
// ──────────────────────────────────────────────

func (s *Server) handleInternalSync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sync request")
		return
	}

	// Merge remote users into our state
	s.mu.Lock()
	now := time.Now()
	for _, u := range req.Users {
		if u.NodeID != s.nodeID {
			uCopy := u
			uCopy.LastSeen = now
			s.users[u.Username] = &uCopy
		}
	}
	localUsers := s.collectOnlineUsers()
	s.mu.Unlock()

	s.broadcastPresenceSync()

	writeJSON(w, http.StatusOK, SyncRequest{NodeID: s.nodeID, Users: localUsers})
}

func (s *Server) handleInternalMessage(w http.ResponseWriter, r *http.Request) {
	var msg Message
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid message")
		return
	}
	if msg.ID == "" || strings.TrimSpace(msg.From) == "" || strings.TrimSpace(msg.To) == "" || strings.TrimSpace(msg.Body) == "" {
		writeError(w, http.StatusBadRequest, "message id, from, to, and body are required")
		return
	}

	s.mu.Lock()
	// Dedup: if we've already seen this message, ignore it
	if s.messageSet[msg.ID] {
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	recipient, isLocal := s.clients[msg.To]
	_, isHomeUser := s.deviceIDs[msg.To]
	if msg.OriginNode == "" {
		if separator := strings.LastIndex(msg.ID, "-"); separator > 0 {
			msg.OriginNode = msg.ID[:separator]
		} else {
			msg.OriginNode = s.nodeID
		}
	}
	if !containsNode(msg.Visited, s.nodeID) {
		msg.Visited = append(msg.Visited, s.nodeID)
	}
	msg.State = "buffered"
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	} else {
		msg.CreatedAt = msg.CreatedAt.UTC()
	}
	if err := s.persistMessage(msg); err != nil {
		s.mu.Unlock()
		writeError(w, http.StatusInternalServerError, "could not persist message")
		return
	}
	s.messageSet[msg.ID] = true
	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	if isLocal {
		recipient.sendJSON(WSMessage{Type: "new_message", Message: &msg})
	}

	// Also notify the sender side if they're local (for cross-node sent messages)
	s.mu.RLock()
	sender, senderLocal := s.clients[msg.From]
	s.mu.RUnlock()
	if senderLocal {
		sender.sendJSON(WSMessage{Type: "new_message", Message: &msg})
	}
	if !isHomeUser && msg.Hops < maxMeshHops {
		go s.forwardMessageToPeers(&msg)
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "accepted"})
}

func (s *Server) handleInternalUserEvent(w http.ResponseWriter, r *http.Request) {
	var event WSMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid event")
		return
	}

	switch event.Type {
	case "user_online":
		if event.User != nil && event.User.NodeID != s.nodeID {
			s.mu.Lock()
			uCopy := *event.User
			uCopy.LastSeen = time.Now()
			s.users[event.User.Username] = &uCopy
			s.mu.Unlock()
			s.broadcastLocal(event, "")
		}
	case "user_offline":
		if event.Username != "" {
			s.mu.Lock()
			if u, ok := s.users[event.Username]; ok && u.NodeID != s.nodeID {
				u.Online = false
				u.LastSeen = time.Now()
			}
			s.mu.Unlock()
			s.broadcastLocal(event, "")
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleInternalCallSignal(w http.ResponseWriter, r *http.Request) {
	var msg WSMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&msg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid call signal")
		return
	}
	if err := validateCallSignal(&msg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg.Hops < 0 || msg.Hops > maxMeshHops {
		writeError(w, http.StatusBadRequest, "invalid hop count")
		return
	}
	if !validUsername(msg.From) {
		writeError(w, http.StatusBadRequest, "valid sender is required")
		return
	}
	if msg.SignalID == "" {
		msg.SignalID = signalFingerprint(msg)
	}
	if !s.markSignalSeen(msg.SignalID) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}

	s.mu.RLock()
	recipient, isLocal := s.clients[msg.To]
	s.mu.RUnlock()

	if isLocal {
		recipient.sendJSON(msg)
	} else if msg.Hops < maxMeshHops {
		go s.forwardCallSignalToPeers(msg)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ──────────────────────────────────────────────
//  JSON Helpers
// ──────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
