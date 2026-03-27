// Package sdp provides minimal SDP (Session Description Protocol) types
// for WebRTC-style peer connections without external dependencies.
package sdp

import (
	"encoding/json"
	"fmt"
	"time"
)

// Type represents the SDP message type
type Type string

const (
	TypeOffer    Type = "offer"
	TypeAnswer   Type = "answer"
	TypeICE      Type = "ice"
	TypeRenegotiate Type = "renegotiate"
	TypeClose    Type = "close"
)

// SessionDescription represents an SDP offer or answer
type SessionDescription struct {
	// Type is either "offer" or "answer"
	Type Type `json:"type"`
	
	// SDP is the session description protocol string
	// Format follows RFC 4566 (simplified)
	SDP string `json:"sdp"`
	
	// Version for protocol evolution
	Version int `json:"version"`
}

// ICECandidate represents an ICE (Interactive Connectivity Establishment) candidate
// Used for NAT traversal
type ICECandidate struct {
	// Type is always "ice"
	Type Type `json:"type"`
	
	// Candidate string in format:
	// candidate:<foundation> <component> <protocol> <priority> <ip> <port> <typ> ...
	Candidate string `json:"candidate"`
	
	// SDPMid identifies the media stream
	SDPMid string `json:"sdpMid,omitempty"`
	
	// SDPMLineIndex is the media line index
	SDPMLineIndex int `json:"sdpMLineIndex,omitempty"`
	
	// UsernameFragment for ICE validation
	UsernameFragment string `json:"usernameFragment,omitempty"`
}

// Signal is the envelope for all P2P signaling messages
type Signal struct {
	// SignalType identifies the type of signal
	SignalType Type `json:"signal_type"`
	
	// Payload is the JSON-encoded signal data
	Payload json.RawMessage `json:"payload"`
	
	// Timestamp when the signal was created
	Timestamp time.Time `json:"timestamp"`
	
	// From is the sender peer ID (set by server)
	From string `json:"from,omitempty"`
	
	// To is the target peer ID
	To string `json:"to,omitempty"`
}

// NewOffer creates a new SDP offer
func NewOffer(sdp string) *SessionDescription {
	return &SessionDescription{
		Type:    TypeOffer,
		SDP:     sdp,
		Version: 1,
	}
}

// NewAnswer creates a new SDP answer
func NewAnswer(sdp string) *SessionDescription {
	return &SessionDescription{
		Type:    TypeAnswer,
		SDP:     sdp,
		Version: 1,
	}
}

// NewICECandidate creates a new ICE candidate
func NewICECandidate(candidate, sdpMid string, sdpMLineIndex int) *ICECandidate {
	return &ICECandidate{
		Type:          TypeICE,
		Candidate:     candidate,
		SDPMid:        sdpMid,
		SDPMLineIndex: sdpMLineIndex,
	}
}

// ToSignal converts a SessionDescription to a Signal
func (s *SessionDescription) ToSignal() (*Signal, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("marshal session description: %w", err)
	}
	
	return &Signal{
		SignalType: s.Type,
		Payload:    payload,
		Timestamp:  time.Now(),
	}, nil
}

// ToSignal converts an ICECandidate to a Signal
func (c *ICECandidate) ToSignal() (*Signal, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal ice candidate: %w", err)
	}
	
	return &Signal{
		SignalType: TypeICE,
		Payload:    payload,
		Timestamp:  time.Now(),
	}, nil
}

// ParseSessionDescription parses a Signal payload into SessionDescription
func ParseSessionDescription(s *Signal) (*SessionDescription, error) {
	var sd SessionDescription
	if err := json.Unmarshal(s.Payload, &sd); err != nil {
		return nil, fmt.Errorf("unmarshal session description: %w", err)
	}
	return &sd, nil
}

// ParseICECandidate parses a Signal payload into ICECandidate
func ParseICECandidate(s *Signal) (*ICECandidate, error) {
	var c ICECandidate
	if err := json.Unmarshal(s.Payload, &c); err != nil {
		return nil, fmt.Errorf("unmarshal ice candidate: %w", err)
	}
	return &c, nil
}

// MediaSection represents a media section in SDP
type MediaSection struct {
	Type      string   `json:"type"`      // "audio", "video", "data"
	Port      int      `json:"port"`
	Protocol  string   `json:"protocol"`  // "UDP/TLS/RTP/SAVPF"
	Formats   []int    `json:"formats"`   // Payload types
	Connection *ConnectionInfo `json:"connection,omitempty"`
}

// ConnectionInfo contains connection address info
type ConnectionInfo struct {
	NetworkType string `json:"networkType"` // "IN"
	AddressType string `json:"addressType"` // "IP4" or "IP6"
	Address     string `json:"address"`
}

// SimplifiedSDP is a structured representation for building/parsing
type SimplifiedSDP struct {
	Version        int            `json:"version"`
	Origin         OriginInfo     `json:"origin"`
	SessionName    string         `json:"sessionName"`
	Timing         TimingInfo     `json:"timing"`
	MediaSections  []MediaSection `json:"mediaSections"`
	Attributes     []Attribute    `json:"attributes"`
}

// OriginInfo contains originator info
type OriginInfo struct {
	Username       string `json:"username"`
	SessionID      int64  `json:"sessionId"`
	SessionVersion int    `json:"sessionVersion"`
	NetworkType    string `json:"networkType"`
	AddressType    string `json:"addressType"`
	Address        string `json:"address"`
}

// TimingInfo contains timing info
type TimingInfo struct {
	Start  int64 `json:"start"`  // 0 for permanent
	Stop   int64 `json:"stop"`   // 0 for permanent
}

// Attribute is an SDP attribute
type Attribute struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// IsOffer returns true if this is an offer
func (s *SessionDescription) IsOffer() bool {
	return s.Type == TypeOffer
}

// IsAnswer returns true if this is an answer
func (s *SessionDescription) IsAnswer() bool {
	return s.Type == TypeAnswer
}

// String returns a human-readable representation
func (s *SessionDescription) String() string {
	return fmt.Sprintf("%s[v%d]: %d bytes", s.Type, s.Version, len(s.SDP))
}
