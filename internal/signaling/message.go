package signaling

import (
	"encoding/json"
)

// MessageType represents the type of signaling message
type MessageType string

const (
	// PeerRegistered is sent when a peer successfully connects and gets an ID
	PeerRegistered MessageType = "peer_registered"
	// PeerList is sent with the list of available peers
	PeerList MessageType = "peer_list"
	// PeerJoined is broadcast when a new peer joins
	PeerJoined MessageType = "peer_joined"
	// PeerLeft is broadcast when a peer disconnects
	PeerLeft MessageType = "peer_left"
	// Signal is used to forward SDP/ICE messages between peers
	Signal MessageType = "signal"
	// Error is sent when something goes wrong
	Error MessageType = "error"
)

// Message is the envelope for all signaling messages
type Message struct {
	Type      MessageType     `json:"type"`
	From      string          `json:"from,omitempty"`
	To        string          `json:"to,omitempty"`
	Data      json.RawMessage `json:"data"`
	Timestamp int64           `json:"timestamp"`
}

// PeerInfo contains information about a peer
type PeerInfo struct {
	ID       string `json:"id"`
	JoinedAt int64  `json:"joined_at"`
}

// PeerListData is sent as data for peer_list messages
type PeerListData struct {
	Peers []PeerInfo `json:"peers"`
}

// SignalData is sent as data for signal messages (SDP/ICE)
type SignalData struct {
	SignalType string `json:"signal_type"` // "offer", "answer", "ice-candidate"
	Payload    string `json:"payload"`     // base64 encoded signal data
}

// PeerRegisteredData is sent when a peer successfully registers
type PeerRegisteredData struct {
	PeerID string `json:"peer_id"`
}

// PeerJoinedLeftData is sent when a peer joins or leaves
type PeerJoinedLeftData struct {
	Peer PeerInfo `json:"peer"`
}

// ErrorData is sent on errors
type ErrorData struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}
