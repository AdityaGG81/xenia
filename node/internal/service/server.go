// Package service contains the core Xenia mesh node server.
//
// Architecture:
//   - Each node maintains WebSocket connections to local clients (phones/browsers)
//   - Nodes discover each other via UDP broadcast on port 9999
//   - Presence and messages are synced between nodes via HTTP internal endpoints
//   - Call signalling (WebRTC SDP/ICE) is relayed between nodes for cross-node calls
package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket tuning constants.
const (
	writeWait          = 10 * time.Second
	pongWait           = 30 * time.Second
	pingPeriod         = 10 * time.Second
	maxMsgSize         = 64 * 1024
	maxMeshHops        = 8
	maxSeenSignals     = 4096
	maxMessageLogBytes = 32 << 20
)

// Client represents a single WebSocket connection from a user.
type Client struct {
	server   *Server
	conn     *websocket.Conn
	deviceID string
	username string
	send     chan []byte
	done     chan struct{}
	stopOnce sync.Once
}

// Server is the core of a Xenia mesh node.
type Server struct {
	nodeID         string
	listenAddr     string
	storageDir     string
	advertiseURL   string
	discoveryIface string
	meshToken      string

	mu          sync.RWMutex
	clients     map[string]*Client // username → active WebSocket client (local only)
	users       map[string]*User   // username → user record (local + remote)
	deviceIDs   map[string]string  // username → stable device UUID
	peers       map[string]*Peer   // apiURL → discovered peer node
	messages    []Message          // chronologically ordered message log
	messageSet  map[string]bool    // message ID deduplication set
	messageAcks map[string]bool
	messageSeq  uint64 // monotonic counter for generating unique message IDs
	seenSignals map[string]time.Time
	startOnce   sync.Once
	ctx         context.Context
	cancel      context.CancelFunc

	httpClient        *http.Client
	storageFile       *os.File
	storageErr        string
	deviceRegistryErr bool
}

// Option configures a Server.
type Option func(*Server)

// WithListenAddr sets the HTTP listen address (e.g. "0.0.0.0:8080").
func WithListenAddr(addr string) Option {
	return func(s *Server) { s.listenAddr = addr }
}

// WithAdvertiseURL sets the mesh-reachable URL announced during peer discovery.
func WithAdvertiseURL(url string) Option {
	return func(s *Server) { s.advertiseURL = strings.TrimRight(url, "/") }
}

// WithDiscoveryInterface restricts UDP peer announcements to one interface.
// The Raspberry Pi bridge uses this to keep discovery on the BATMAN mesh.
func WithDiscoveryInterface(name string) Option {
	return func(s *Server) { s.discoveryIface = strings.TrimSpace(name) }
}

// WithMeshToken enables shared-secret authentication on internal mesh APIs.
func WithMeshToken(token string) Option {
	return func(s *Server) { s.meshToken = token }
}

// WithPeerTLSConfig configures trust for HTTPS calls between mesh nodes.
func WithPeerTLSConfig(config *tls.Config) Option {
	return func(s *Server) {
		if config == nil {
			return
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = config.Clone()
		s.httpClient = &http.Client{Timeout: 3 * time.Second, Transport: transport}
	}
}

// WithStorageDir sets the directory for persistent message storage.
func WithStorageDir(dir string) Option {
	return func(s *Server) { s.storageDir = dir }
}

// New creates a new Xenia mesh node server.
func New(nodeID string, opts ...Option) *Server {
	if strings.TrimSpace(nodeID) == "" {
		nodeID = "node"
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		nodeID:      nodeID,
		clients:     make(map[string]*Client),
		users:       make(map[string]*User),
		deviceIDs:   make(map[string]string),
		peers:       make(map[string]*Peer),
		messageSet:  make(map[string]bool),
		messageAcks: make(map[string]bool),
		seenSignals: make(map[string]time.Time),
		ctx:         ctx,
		cancel:      cancel,
		httpClient:  &http.Client{Timeout: 3 * time.Second},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.loadMessages()
	s.loadDeviceIDs()
	return s
}

func newDeviceID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}

func (s *Server) loadDeviceIDs() {
	if s.storageDir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(s.storageDir, "devices.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		s.storageErr = err.Error()
		s.deviceRegistryErr = true
		log.Printf("[%s] read device registry: %v", s.nodeID, err)
		return
	}
	if err := json.Unmarshal(b, &s.deviceIDs); err != nil {
		s.storageErr = err.Error()
		s.deviceRegistryErr = true
		log.Printf("[%s] decode device registry: %v", s.nodeID, err)
		s.deviceIDs = make(map[string]string)
	}
}

func (s *Server) saveDeviceIDsLocked() error {
	if s.storageDir == "" {
		return nil
	}
	b, err := json.Marshal(s.deviceIDs)
	if err != nil {
		return err
	}
	path := filepath.Join(s.storageDir, "devices.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) registerDevice(username, requestedID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deviceRegistryErr {
		return "", fmt.Errorf("device registry is unreadable; repair devices.json before registering")
	}
	if id := s.deviceIDs[username]; id != "" {
		if requestedID != "" && requestedID != id {
			return "", fmt.Errorf("username is already registered to another device")
		}
		return id, nil
	}
	id := requestedID
	if id == "" {
		var err error
		id, err = newDeviceID()
		if err != nil {
			return "", err
		}
	} else if !validDeviceID(id) {
		return "", fmt.Errorf("device_id must be a UUID")
	}
	for existingName, existingID := range s.deviceIDs {
		if existingID == id && existingName != username {
			return "", fmt.Errorf("device_id is already registered")
		}
	}
	s.deviceIDs[username] = id
	if err := s.saveDeviceIDsLocked(); err != nil {
		s.storageErr = err.Error()
		delete(s.deviceIDs, username)
		return "", err
	}
	return id, nil
}

func validDeviceID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	compact := strings.ReplaceAll(id, "-", "")
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16
}

func (s *Server) postPeerJSON(peerURL, endpoint string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(peerURL, "/")+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.meshToken != "" {
		req.Header.Set("X-Xenia-Mesh-Token", s.meshToken)
	}
	return s.httpClient.Do(req)
}

// ──────────────────────────────────────────────
//  Storage
// ──────────────────────────────────────────────

func (s *Server) loadMessages() {
	if s.storageDir == "" {
		return
	}
	if err := os.MkdirAll(s.storageDir, 0750); err != nil {
		s.storageErr = err.Error()
		log.Printf("[%s] create storage directory: %v", s.nodeID, err)
		return
	}
	path := filepath.Join(s.storageDir, "messages.jsonl")

	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.storageErr = err.Error()
			log.Printf("[%s] open message log: %v", s.nodeID, err)
			return
		}
		s.storageFile, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
		if err != nil {
			s.storageErr = err.Error()
			log.Printf("[%s] create message log: %v", s.nodeID, err)
		}
		return
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	seen := make(map[string]Message)
	for scanner.Scan() {
		var m Message
		if json.Unmarshal(scanner.Bytes(), &m) == nil && m.ID != "" {
			seen[m.ID] = m
			s.messageSet[m.ID] = true
			if strings.HasPrefix(m.ID, s.nodeID+"-") {
				var num uint64
				fmt.Sscanf(strings.TrimPrefix(m.ID, s.nodeID+"-"), "%d", &num)
				if num > s.messageSeq {
					s.messageSeq = num
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		s.storageErr = err.Error()
		log.Printf("[%s] read message log: %v", s.nodeID, err)
	}
	f.Close()

	for _, m := range seen {
		s.messages = append(s.messages, m)
	}
	sort.Slice(s.messages, func(i, j int) bool {
		return s.messages[i].CreatedAt.Before(s.messages[j].CreatedAt)
	})

	s.storageFile, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		s.storageErr = err.Error()
		log.Printf("[%s] reopen message log for append: %v", s.nodeID, err)
	}
	log.Printf("[%s] loaded %d messages from disk", s.nodeID, len(s.messages))
}

func (s *Server) persistMessage(m Message) error {
	if s.storageFile == nil {
		if s.storageDir != "" {
			return fmt.Errorf("message storage is unavailable")
		}
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := s.storageFile.Write(b); err != nil {
		s.storageErr = err.Error()
		return err
	}
	if err := s.storageFile.Sync(); err != nil {
		s.storageErr = err.Error()
		return err
	}
	info, err := s.storageFile.Stat()
	if err == nil && info.Size() > maxMessageLogBytes {
		if compactErr := s.compactMessageLog(); compactErr != nil {
			log.Printf("[%s] compact message log: %v", s.nodeID, compactErr)
		}
		return nil
	}
	return err
}

// compactMessageLog keeps the latest state for each message and bounds the append-only log.
// Callers must hold s.mu.
func (s *Server) compactMessageLog() error {
	path := filepath.Join(s.storageDir, "messages.jsonl")
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, message := range s.messages {
		line, marshalErr := json.Marshal(message)
		if marshalErr != nil {
			_ = f.Close()
			return marshalErr
		}
		if _, err = w.Write(append(line, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err = w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	old := s.storageFile
	s.storageFile, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0640)
	if old != nil {
		_ = old.Close()
	}
	return err
}

// ──────────────────────────────────────────────
//  Client Management
// ──────────────────────────────────────────────

func (s *Server) registerClient(c *Client) {
	s.mu.Lock()

	// Close any existing connection for this username (prevents duplicates)
	if old, ok := s.clients[c.username]; ok {
		log.Printf("[%s] replacing existing connection for %s", s.nodeID, c.username)
		old.stop()
	}

	s.clients[c.username] = c

	now := time.Now()
	user := &User{
		DeviceID: c.deviceID,
		Username: c.username,
		NodeID:   s.nodeID,
		Online:   true,
		LastSeen: now,
	}
	s.users[c.username] = user

	onlineUsers := s.collectOnlineUsers()
	history := s.messagesForUser(c.username)
	userCopy := *user
	s.mu.Unlock()

	// Send full presence snapshot to the new client
	c.sendJSON(WSMessage{Type: "presence_sync", Users: onlineUsers})

	// Send chat history
	if len(history) > 0 {
		c.sendJSON(WSMessage{Type: "message_history", Messages: history})
	}

	// Notify all other local clients
	s.broadcastLocal(WSMessage{Type: "user_online", User: &userCopy}, c.username)

	// Notify all peer nodes
	go s.notifyPeersUserEvent("user_online", &userCopy)

	log.Printf("[%s] ✓ %s connected", s.nodeID, c.username)
}

func (c *Client) stop() {
	c.stopOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// Close stops background discovery and closes active client and storage resources.
func (s *Server) Close() error {
	s.cancel()
	s.mu.Lock()
	clients := make([]*Client, 0, len(s.clients))
	for _, client := range s.clients {
		clients = append(clients, client)
	}
	file := s.storageFile
	s.storageFile = nil
	s.mu.Unlock()
	for _, client := range clients {
		client.stop()
	}
	s.httpClient.CloseIdleConnections()
	if file != nil {
		return file.Close()
	}
	return nil
}

func (s *Server) unregisterClient(c *Client) {
	wasActive := false
	s.mu.Lock()
	// Only remove if this is still the active client for this username
	if current, ok := s.clients[c.username]; ok && current == c {
		wasActive = true
		delete(s.clients, c.username)
		if user, ok := s.users[c.username]; ok {
			user.Online = false
			user.LastSeen = time.Now()
		}
	}
	s.mu.Unlock()
	if !wasActive {
		return
	}

	s.broadcastLocal(WSMessage{Type: "user_offline", Username: c.username}, "")

	go s.notifyPeersUserEvent("user_offline", &User{
		Username: c.username,
		NodeID:   s.nodeID,
		Online:   false,
		LastSeen: time.Now(),
	})

	log.Printf("[%s] ✗ %s disconnected", s.nodeID, c.username)
}

// ──────────────────────────────────────────────
//  WebSocket Message Dispatch
// ──────────────────────────────────────────────

func (s *Server) handleWSMessage(c *Client, msg *WSMessage) {
	// Every message counts as a heartbeat
	s.mu.Lock()
	if user, ok := s.users[c.username]; ok {
		user.LastSeen = time.Now()
	}
	s.mu.Unlock()

	switch msg.Type {
	case "heartbeat":
		// Already handled above
	case "send_message":
		s.handleChatMessage(c, msg)
	case "message_ack":
		s.handleMessageAck(c, msg.MessageID)
	case "call_offer", "call_answer", "call_hangup", "call_reject", "ice_candidate":
		if err := validateCallSignal(msg); err != nil {
			c.sendJSON(WSMessage{Type: "error", Error: err.Error()})
			return
		}
		s.handleCallSignal(c, msg)
	default:
		c.sendJSON(WSMessage{Type: "error", Error: "unknown event type"})
	}
}

func validateCallSignal(msg *WSMessage) error {
	if !validUsername(msg.To) {
		return fmt.Errorf("valid recipient is required")
	}
	if len(msg.CallID) == 0 || len(msg.CallID) > 128 {
		return fmt.Errorf("valid call_id is required")
	}
	if len(msg.SignalID) > 128 {
		return fmt.Errorf("signal_id is too long")
	}
	switch msg.Type {
	case "call_offer", "call_answer":
		if strings.TrimSpace(msg.SDP) == "" || len(msg.SDP) > maxMsgSize/2 {
			return fmt.Errorf("valid SDP is required")
		}
	case "ice_candidate":
		if strings.TrimSpace(msg.Candidate) == "" || len(msg.Candidate) > 8192 {
			return fmt.Errorf("valid ICE candidate is required")
		}
	case "call_hangup", "call_reject":
	default:
		return fmt.Errorf("unsupported call signal type")
	}
	return nil
}

// reapUsers marks local sessions offline when three five-second heartbeats are missed.
func (s *Server) reapUsers() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			var expired []*Client
			s.mu.RLock()
			for _, client := range s.clients {
				if user := s.users[client.username]; user != nil && now.Sub(user.LastSeen) > 15*time.Second {
					expired = append(expired, client)
				}
			}
			s.mu.RUnlock()
			for _, client := range expired {
				client.stop()
			}
		}
	}
}

// ──────────────────────────────────────────────
//  Chat Messaging
// ──────────────────────────────────────────────

func (s *Server) handleChatMessage(sender *Client, msg *WSMessage) {
	if !validUsername(msg.To) || strings.TrimSpace(msg.Body) == "" {
		sender.sendJSON(WSMessage{Type: "error", Error: "to and body are required"})
		return
	}
	if len(msg.Body) > maxMsgSize/2 {
		sender.sendJSON(WSMessage{Type: "error", Error: "message is too large"})
		return
	}

	s.mu.Lock()
	s.messageSeq++
	message := Message{
		ID:         fmt.Sprintf("%s-%d", s.nodeID, s.messageSeq),
		OriginNode: s.nodeID,
		Visited:    []string{s.nodeID},
		From:       sender.username,
		To:         msg.To,
		Body:       msg.Body,
		CreatedAt:  time.Now().UTC(),
		State:      "sent",
	}
	if err := s.persistMessage(message); err != nil {
		s.mu.Unlock()
		sender.sendJSON(WSMessage{Type: "error", Error: "message could not be saved"})
		return
	}
	s.messageSet[message.ID] = true
	s.messages = append(s.messages, message)
	recipient, isLocal := s.clients[msg.To]
	_, isHomeUser := s.deviceIDs[msg.To]
	s.mu.Unlock()

	// Confirm to sender
	sender.sendJSON(WSMessage{Type: "message_sent", Message: &message})

	// Deliver locally if recipient is on this node
	if isLocal {
		recipient.sendJSON(WSMessage{Type: "new_message", Message: &message})
	}

	// Always forward to peers (the recipient might be on another node)
	if !isHomeUser {
		go s.forwardMessageToPeers(&message)
	}
}

func (s *Server) forwardMessageToPeers(msg *Message) {
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()

	if msg.Hops >= maxMeshHops {
		return
	}
	forward := *msg
	if !containsNode(forward.Visited, s.nodeID) {
		forward.Visited = append(forward.Visited, s.nodeID)
	}
	forward.Hops++
	accepted := false
	for _, peer := range peers {
		if containsNode(forward.Visited, peer.NodeID) {
			continue
		}
		resp, err := s.postPeerJSON(peer.APIURL, "/internal/message", forward)
		if err != nil {
			log.Printf("[%s] failed to forward msg to %s: %v", s.nodeID, peer.NodeID, err)
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			log.Printf("[%s] peer %s rejected message: %s", s.nodeID, peer.NodeID, resp.Status)
		} else {
			accepted = true
		}
		resp.Body.Close()
	}
	if accepted && (msg.State == "sent" || msg.State == "buffered") {
		s.updateMessageState(msg.ID, "forwarded")
	}
}

func containsNode(nodes []string, nodeID string) bool {
	for _, node := range nodes {
		if node == nodeID {
			return true
		}
	}
	return false
}

func (s *Server) updateMessageState(messageID, state string) {
	s.mu.Lock()
	var message *Message
	var sender *Client
	for i := range s.messages {
		if s.messages[i].ID == messageID && s.messages[i].State != state {
			s.messages[i].State = state
			copy := s.messages[i]
			message = &copy
			sender = s.clients[copy.From]
			if err := s.persistMessage(s.messages[i]); err != nil {
				log.Printf("[%s] persist state for message %s: %v", s.nodeID, messageID, err)
			}
			break
		}
	}
	s.mu.Unlock()
	if sender != nil && message != nil {
		sender.sendJSON(WSMessage{Type: "message_status", Message: message})
	}
}

func (s *Server) handleMessageAck(client *Client, messageID string) {
	if strings.TrimSpace(messageID) == "" {
		return
	}
	s.mu.Lock()
	var message *Message
	for i := range s.messages {
		if s.messages[i].ID == messageID && s.messages[i].To == client.username {
			s.messages[i].State = "delivered"
			copy := s.messages[i]
			message = &copy
			if err := s.persistMessage(copy); err != nil {
				log.Printf("[%s] persist ack %s: %v", s.nodeID, messageID, err)
			}
			break
		}
	}
	if message == nil {
		s.mu.Unlock()
		return
	}
	ack := MessageAck{MessageID: message.ID, OriginNode: message.OriginNode, Visited: []string{s.nodeID}}
	if ack.OriginNode == "" {
		ack.OriginNode = s.nodeID
	}
	first := !s.messageAcks[messageID]
	s.messageAcks[messageID] = true
	s.mu.Unlock()
	if sender := s.localClient(message.From); sender != nil {
		sender.sendJSON(WSMessage{Type: "message_status", Message: message})
	}
	if first && ack.OriginNode != s.nodeID {
		go s.forwardMessageAck(ack)
	}
}

func (s *Server) localClient(username string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clients[username]
}

func (s *Server) forwardMessageAck(ack MessageAck) {
	if ack.Hops >= maxMeshHops {
		return
	}
	if !containsNode(ack.Visited, s.nodeID) {
		ack.Visited = append(ack.Visited, s.nodeID)
	}
	ack.Hops++
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()
	for _, peer := range peers {
		if containsNode(ack.Visited, peer.NodeID) {
			continue
		}
		resp, err := s.postPeerJSON(peer.APIURL, "/internal/message-ack", ack)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}

func (s *Server) retryPendingMessages() {
	s.mu.RLock()
	pending := make([]Message, 0)
	for _, msg := range s.messages {
		if (msg.State == "sent" || msg.State == "buffered") && msg.Hops < maxMeshHops {
			pending = append(pending, msg)
		}
	}
	s.mu.RUnlock()
	for i := range pending {
		msg := pending[i]
		go s.forwardMessageToPeers(&msg)
	}
}

// ──────────────────────────────────────────────
//  WebRTC Call Signalling
// ──────────────────────────────────────────────

func (s *Server) handleCallSignal(sender *Client, msg *WSMessage) {
	if strings.TrimSpace(msg.To) == "" {
		return
	}

	// Stamp the sender identity so the recipient knows who this is from
	relay := *msg
	relay.From = sender.username
	relay.Hops = 0
	if relay.SignalID == "" {
		relay.SignalID = signalFingerprint(relay)
	}
	if !s.markSignalSeen(relay.SignalID) {
		return
	}

	s.mu.RLock()
	recipient, isLocal := s.clients[relay.To]
	s.mu.RUnlock()

	if isLocal {
		recipient.sendJSON(relay)
	} else {
		go s.forwardCallSignalToPeers(relay)
	}
}

func signalFingerprint(msg WSMessage) string {
	msg.SignalID = ""
	msg.Hops = 0
	b, _ := json.Marshal(msg)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Server) markSignalSeen(id string) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for knownID, seenAt := range s.seenSignals {
		if now.Sub(seenAt) > 10*time.Minute {
			delete(s.seenSignals, knownID)
		}
	}
	if _, exists := s.seenSignals[id]; exists {
		return false
	}
	for len(s.seenSignals) >= maxSeenSignals {
		var oldestID string
		var oldestAt time.Time
		for knownID, seenAt := range s.seenSignals {
			if oldestID == "" || seenAt.Before(oldestAt) {
				oldestID, oldestAt = knownID, seenAt
			}
		}
		delete(s.seenSignals, oldestID)
	}
	s.seenSignals[id] = now
	return true
}

func (s *Server) forwardCallSignalToPeers(msg WSMessage) {
	if msg.Hops >= maxMeshHops {
		return
	}
	msg.Hops++
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()
	for _, peer := range peers {
		resp, err := s.postPeerJSON(peer.APIURL, "/internal/call-signal", msg)
		if err != nil {
			log.Printf("[%s] failed to relay call signal to %s: %v", s.nodeID, peer.NodeID, err)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("[%s] peer %s rejected call signal: %s", s.nodeID, peer.NodeID, resp.Status)
		}
		resp.Body.Close()
	}
}

// ──────────────────────────────────────────────
//  Broadcasting
// ──────────────────────────────────────────────

func (s *Server) broadcastLocal(msg WSMessage, excludeUsername string) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for username, client := range s.clients {
		if username != excludeUsername {
			select {
			case client.send <- data:
			default:
				log.Printf("[%s] closing slow client %s (send queue full)", s.nodeID, username)
				client.stop()
			}
		}
	}
}

func (s *Server) broadcastPresenceSync() {
	s.mu.RLock()
	users := s.collectOnlineUsers()
	s.mu.RUnlock()
	s.broadcastLocal(WSMessage{Type: "presence_sync", Users: users}, "")
}

// ──────────────────────────────────────────────
//  Helpers
// ──────────────────────────────────────────────

// collectOnlineUsers returns all users currently marked online. Must be called with s.mu held.
func (s *Server) collectOnlineUsers() []User {
	users := make([]User, 0, len(s.users))
	for _, u := range s.users {
		if u.Online {
			users = append(users, *u)
		}
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users
}

// localOnlineUsers returns users connected to THIS node. Must be called with s.mu held.
func (s *Server) localOnlineUsers() []User {
	users := make([]User, 0, len(s.clients))
	for _, c := range s.clients {
		if u, ok := s.users[c.username]; ok {
			users = append(users, *u)
		}
	}
	return users
}

func (s *Server) messagesForUser(username string) []Message {
	var result []Message
	for _, msg := range s.messages {
		if msg.From == username || msg.To == username {
			result = append(result, msg)
		}
	}
	return result
}

// activePeers returns peers seen in the last 30 seconds. Must be called with s.mu held.
func (s *Server) activePeers() []*Peer {
	now := time.Now()
	var peers []*Peer
	for _, p := range s.peers {
		if now.Sub(p.LastSeen) < 30*time.Second {
			peers = append(peers, p)
		}
	}
	return peers
}

// ──────────────────────────────────────────────
//  Peer Notification
// ──────────────────────────────────────────────

func (s *Server) notifyPeersUserEvent(eventType string, user *User) {
	s.mu.RLock()
	peers := s.activePeers()
	s.mu.RUnlock()

	event := WSMessage{Type: eventType}
	if eventType == "user_online" {
		event.User = user
	} else {
		event.Username = user.Username
	}

	for _, peer := range peers {
		resp, err := s.postPeerJSON(peer.APIURL, "/internal/user-event", event)
		if err != nil {
			log.Printf("[%s] failed to notify peer %s: %v", s.nodeID, peer.NodeID, err)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			log.Printf("[%s] peer %s rejected presence event: %s", s.nodeID, peer.NodeID, resp.Status)
		}
		resp.Body.Close()
	}
}

func (s *Server) syncWithPeer(peerURL string) {
	s.mu.RLock()
	localUsers := s.collectOnlineUsers()
	s.mu.RUnlock()

	resp, err := s.postPeerJSON(peerURL, "/internal/sync", SyncRequest{NodeID: s.nodeID, Users: localUsers})
	if err != nil {
		log.Printf("[%s] sync with %s failed: %v", s.nodeID, peerURL, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[%s] sync with %s returned %s", s.nodeID, peerURL, resp.Status)
		return
	}

	var peerResp SyncRequest
	if err := json.NewDecoder(resp.Body).Decode(&peerResp); err != nil {
		log.Printf("[%s] invalid sync response from %s: %v", s.nodeID, peerURL, err)
		return
	}

	s.mu.Lock()
	changed := false
	for _, u := range peerResp.Users {
		if u.NodeID != s.nodeID {
			uCopy := u
			existing, exists := s.users[u.Username]
			if !exists || u.LastSeen.After(existing.LastSeen) {
				s.users[u.Username] = &uCopy
				changed = true
			}
		}
	}
	s.mu.Unlock()

	log.Printf("[%s] synced with %s: got %d users", s.nodeID, peerResp.NodeID, len(peerResp.Users))
	if changed {
		s.broadcastPresenceSync()
	}
}

// ──────────────────────────────────────────────
//  UDP Discovery
// ──────────────────────────────────────────────

// StartDiscovery begins the UDP broadcaster, listener, and periodic sync goroutines.
func (s *Server) StartDiscovery() {
	s.startOnce.Do(func() {
		go s.reapUsers()
		go s.listenUDP()
		go s.broadcastUDP()
		go s.periodicSync()
	})
}

// StartUDPDiscovery is retained for the node command's existing startup call.
func (s *Server) StartUDPDiscovery() { s.StartDiscovery() }

func (s *Server) listenUDP() {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: 9999}
	for s.ctx.Err() == nil {
		conn, err := net.ListenUDP("udp4", addr)
		if err != nil {
			log.Printf("[%s] UDP discovery listen failed: %v; retrying", s.nodeID, err)
			if !s.waitDiscoveryRetry() {
				return
			}
			continue
		}
		log.Printf("[%s] UDP discovery listening on :9999", s.nodeID)
		if err := s.readUDP(conn); err != nil && s.ctx.Err() == nil {
			log.Printf("[%s] UDP discovery listener stopped: %v; retrying", s.nodeID, err)
		}
		_ = conn.Close()
		if !s.waitDiscoveryRetry() {
			return
		}
	}
}

func (s *Server) readUDP(conn *net.UDPConn) error {
	buf := make([]byte, 2048)
	for s.ctx.Err() == nil {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return err
		}
		var pkt DiscoveryPacket
		if json.Unmarshal(buf[:n], &pkt) != nil || pkt.NodeID == s.nodeID || strings.TrimSpace(pkt.NodeID) == "" || !validPeerAPIURL(pkt.APIURL) {
			continue
		}

		s.mu.Lock()
		peer, known := s.peers[pkt.APIURL]
		if known && peer.NodeID != pkt.NodeID {
			log.Printf("[%s] ignoring conflicting discovery identity for %s", s.nodeID, pkt.APIURL)
			s.mu.Unlock()
			continue
		}
		s.peers[pkt.APIURL] = &Peer{
			NodeID:   pkt.NodeID,
			APIURL:   pkt.APIURL,
			LastSeen: time.Now(),
		}
		s.mu.Unlock()

		if !known {
			log.Printf("[%s] discovered peer %s at %s", s.nodeID, pkt.NodeID, pkt.APIURL)
			go s.syncWithPeer(pkt.APIURL)
		}
	}
	return nil
}

func (s *Server) waitDiscoveryRetry() bool {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func validPeerAPIURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return false
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return false
		}
	}
	return true
}

func (s *Server) broadcastUDP() {
	apiURL := s.resolveAPIURL()
	pkt := DiscoveryPacket{NodeID: s.nodeID, APIURL: apiURL}
	data, _ := json.Marshal(pkt)
	log.Printf("[%s] advertising %s via UDP discovery", s.nodeID, apiURL)
	s.broadcastToAllSubnets(data)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.broadcastToAllSubnets(data)
		}
	}
}

func (s *Server) broadcastToAllSubnets(data []byte) {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("[%s] list network interfaces for discovery: %v", s.nodeID, err)
		return
	}
	sent := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if s.discoveryIface != "" && iface.Name != s.discoveryIface {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("[%s] read addresses for discovery interface %s: %v", s.nodeID, iface.Name, err)
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil || len(ipnet.Mask) != net.IPv4len {
				continue
			}
			ip := ipnet.IP.To4()
			broadcast := make(net.IP, net.IPv4len)
			for i := range broadcast {
				broadcast[i] = ip[i] | ^ipnet.Mask[i]
			}
			if broadcast.Equal(ip) {
				continue
			}
			if err := sendUDPBroadcast(ip, broadcast, data); err != nil {
				log.Printf("[%s] UDP discovery broadcast on %s (%s): %v", s.nodeID, iface.Name, broadcast, err)
				continue
			}
			sent++
		}
	}
	if s.discoveryIface != "" && sent == 0 {
		log.Printf("[%s] no IPv4 broadcast address on discovery interface %q", s.nodeID, s.discoveryIface)
	}
}

func sendUDPBroadcast(sourceIP, broadcastIP net.IP, data []byte) error {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: sourceIP.To4()})
	if err != nil {
		return err
	}
	defer conn.Close()
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		socketErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err = conn.WriteToUDP(data, &net.UDPAddr{IP: broadcastIP.To4(), Port: 9999})
	return err
}

func (s *Server) resolveAPIURL() string {
	if s.advertiseURL != "" {
		return s.advertiseURL
	}
	_, port, err := net.SplitHostPort(s.listenAddr)
	if err != nil || port == "" {
		port = "8080"
	}
	if host, _, err := net.SplitHostPort(s.listenAddr); err == nil && host != "" && host != "0.0.0.0" && host != "::" {
		return "http://" + net.JoinHostPort(host, port)
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "http://localhost:" + port
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return "http://" + net.JoinHostPort(ipnet.IP.String(), port)
		}
	}
	return "http://localhost:" + port
}

func (s *Server) periodicSync() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			peers := s.activePeers()
			s.mu.RUnlock()

			for _, peer := range peers {
				s.syncWithPeer(peer.APIURL)
			}
			s.retryPendingMessages()

			// Expire stale remote users (not seen by their home node for >45s)
			s.mu.Lock()
			now := time.Now()
			presenceChanged := false
			for username, user := range s.users {
				if user.NodeID != s.nodeID && now.Sub(user.LastSeen) > 45*time.Second {
					delete(s.users, username)
					presenceChanged = true
				}
			}
			for peerURL, peer := range s.peers {
				if now.Sub(peer.LastSeen) > 45*time.Second {
					delete(s.peers, peerURL)
				}
			}
			s.mu.Unlock()
			if presenceChanged {
				s.broadcastPresenceSync()
			}
		}
	}
}

// ──────────────────────────────────────────────
//  WebSocket Client Read/Write Pumps
// ──────────────────────────────────────────────

func (c *Client) sendJSON(msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
		log.Printf("[%s] closing slow client %s (send queue full)", c.server.nodeID, c.username)
		c.stop()
	}
}

func (c *Client) readPump() {
	defer func() {
		c.server.unregisterClient(c)
		c.stop()
	}()

	c.conn.SetReadLimit(maxMsgSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[%s] ws read error from %s: %v", c.server.nodeID, c.username, err)
			}
			return
		}
		var msg WSMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		c.server.handleWSMessage(c, &msg)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case data, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
