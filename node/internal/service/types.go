package service

import "time"

// User represents a participant in the mesh network.
type User struct {
	DeviceID string    `json:"device_id"`
	Username string    `json:"username"`
	NodeID   string    `json:"node_id"`
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen"`
}

// Message represents a chat message exchanged between users.
type Message struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	State      string    `json:"state"` // sent, delivered
	OriginNode string    `json:"origin_node,omitempty"`
	Hops       int       `json:"hops,omitempty"`
	Visited    []string  `json:"visited,omitempty"`
}

// Peer represents a discovered node in the mesh.
type Peer struct {
	NodeID   string    `json:"node_id"`
	APIURL   string    `json:"api_url"`
	LastSeen time.Time `json:"last_seen"`
}

// WSMessage is the unified envelope for all WebSocket communication.
// Fields are populated selectively based on the Type value.
type WSMessage struct {
	Type string `json:"type"`

	// Presence fields
	User     *User  `json:"user,omitempty"`
	Users    []User `json:"users,omitempty"`
	Username string `json:"username,omitempty"`
	DeviceID string `json:"device_id,omitempty"`

	// Chat fields
	To        string    `json:"to,omitempty"`
	Body      string    `json:"body,omitempty"`
	Message   *Message  `json:"message,omitempty"`
	Messages  []Message `json:"messages,omitempty"`
	MessageID string    `json:"message_id,omitempty"`

	// WebRTC call signalling fields
	CallID    string `json:"call_id,omitempty"`
	From      string `json:"from,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`

	// Error reporting
	Error string `json:"error,omitempty"`

	// Mesh forwarding metadata
	SignalID string `json:"signal_id,omitempty"`
	Hops     int    `json:"hops,omitempty"`
}

// SyncRequest is the payload for node-to-node full state synchronization.
type SyncRequest struct {
	NodeID string `json:"node_id"`
	Users  []User `json:"users"`
}

type MessageAck struct {
	MessageID  string   `json:"message_id"`
	OriginNode string   `json:"origin_node"`
	Hops       int      `json:"hops,omitempty"`
	Visited    []string `json:"visited,omitempty"`
}

// DiscoveryPacket is the UDP broadcast payload for node discovery.
type DiscoveryPacket struct {
	NodeID string `json:"node_id"`
	APIURL string `json:"api_url"`
}
