// Package p2p provides a high-level client library for P2P signaling.
//
// Basic usage:
//
//	client, err := p2p.Connect("ws://localhost:8080/ws", nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	// Handle incoming signals
//	client.OnSignal(func(s *sdp.Signal) {
//	    switch s.SignalType {
//	    case sdp.TypeOffer:
//	        offer, _ := sdp.ParseSessionDescription(s)
//	        // Handle offer...
//	    }
//	})
//
//	// Send an offer to a peer
//	offer := sdp.NewOffer(sdpString)
//	client.SendOffer("peer-id", offer)
//
package p2p

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"p2phub/pkg/p2p/ice"
	"p2phub/pkg/p2p/sdp"
	"p2phub/pkg/p2p/transport"
)

// Handler is called when a signal is received from a peer
type Handler func(from string, signal *sdp.Signal)

// PeerHandler is called when peer events occur
type PeerHandler func(event PeerEvent)

// PeerEvent represents a peer-related event
type PeerEvent struct {
	Type  string
	PeerID string
	Peers []string // For list events
}

// Options for configuring the client
type Options struct {
	Logger *slog.Logger
}

// Client is a high-level P2P signaling client
type Client struct {
	transport   *transport.WebSocketClient
	handler     Handler
	peerHandler PeerHandler
	mu          sync.RWMutex
	peers       map[string]bool
}

// Connect creates a new client and connects to the signaling server
func Connect(serverURL string, opts *Options) (*Client, error) {
	logger := slog.Default()
	if opts != nil && opts.Logger != nil {
		logger = opts.Logger
	}

	config := transport.DefaultConfig(serverURL)
	config.Logger = logger

	wsClient := transport.NewWebSocketClient(config)

	client := &Client{
		transport: wsClient,
		peers:     make(map[string]bool),
	}

	// Set up transport handlers
	wsClient.SetHandler(client.handleTransportMessage)
	wsClient.SetPeerHandler(client.handlePeerEvent)

	if err := wsClient.Connect(); err != nil {
		return nil, fmt.Errorf("connect to server: %w", err)
	}

	return client, nil
}

// Close disconnects from the server
func (c *Client) Close() error {
	return c.transport.Close()
}

// MyID returns this client's peer ID
func (c *Client) MyID() string {
	return c.transport.MyID()
}

// SendOffer sends an SDP offer to a peer
func (c *Client) SendOffer(to string, offer *sdp.SessionDescription) error {
	sig, err := offer.ToSignal()
	if err != nil {
		return fmt.Errorf("convert offer to signal: %w", err)
	}
	return c.transport.Send(to, sig)
}

// SendAnswer sends an SDP answer to a peer
func (c *Client) SendAnswer(to string, answer *sdp.SessionDescription) error {
	sig, err := answer.ToSignal()
	if err != nil {
		return fmt.Errorf("convert answer to signal: %w", err)
	}
	return c.transport.Send(to, sig)
}

// SendICECandidate sends an ICE candidate to a peer (old SDP format)
func (c *Client) SendICECandidate(to string, candidate *sdp.ICECandidate) error {
	sig, err := candidate.ToSignal()
	if err != nil {
		return fmt.Errorf("convert candidate to signal: %w", err)
	}
	return c.transport.Send(to, sig)
}

// SendICECandidateJSON sends an ICE candidate (new JSON format)
func (c *Client) SendICECandidateJSON(to string, candidate *ice.Candidate) error {
	data, err := ice.CandidateToJSON(candidate)
	if err != nil {
		return fmt.Errorf("marshal candidate: %w", err)
	}
	
	// Wrap in ICECandidateMsg
	iceMsg := ICECandidateMsg{
		Type:      "ice-candidate",
		Candidate: data,
	}
	
	// Marshal the ICE message
	payload, err := json.Marshal(iceMsg)
	if err != nil {
		return fmt.Errorf("marshal ice msg: %w", err)
	}
	
	// Wrap in sdp.Signal envelope
	sig := &sdp.Signal{
		SignalType: sdp.TypeICE,
		Payload:    payload,
		Timestamp:  time.Now(),
	}
	
	return c.transport.Send(to, sig)
}

// ICECandidateMsg is the message format for ICE candidates
type ICECandidateMsg struct {
	Type      string          `json:"type"`
	Candidate json.RawMessage `json:"candidate"`
}

// NewICEAgent creates a new ICE agent for gathering local candidates
func (c *Client) NewICEAgent(config ice.AgentConfig) *ice.Agent {
	if config.Logger == nil {
		config.Logger = c.transport.Logger()
	}
	return ice.NewAgent(config)
}

// OnSignal sets the handler for incoming signals
func (c *Client) OnSignal(h Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handler = h
}

// OnPeerEvent sets the handler for peer events
func (c *Client) OnPeerEvent(h PeerHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.peerHandler = h
}

// Peers returns a list of connected peer IDs
func (c *Client) Peers() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	peers := make([]string, 0, len(c.peers))
	for id := range c.peers {
		peers = append(peers, id)
	}
	return peers
}

func (c *Client) handleTransportMessage(msg transport.Message) {
	// Parse the embedded signal from the message data
	sig := &sdp.Signal{}
	if err := json.Unmarshal(msg.Data, sig); err != nil {
		// If it's not a valid signal, create a simple one with raw data
		sig = &sdp.Signal{
			SignalType: sdp.Type(msg.Type),
			Payload:    msg.Data,
		}
	}
	sig.From = msg.From

	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()

	if handler != nil {
		handler(msg.From, sig)
	}
}

func (c *Client) handlePeerEvent(event transport.PeerEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch event.Type {
	case "joined":
		c.peers[event.Peer.ID] = true
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{
				Type:   "joined",
				PeerID: event.Peer.ID,
			})
		}
	case "left":
		delete(c.peers, event.Peer.ID)
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{
				Type:   "left",
				PeerID: event.Peer.ID,
			})
		}
	case "list":
		peers := make([]string, len(event.Peers))
		for i, p := range event.Peers {
			c.peers[p.ID] = true
			peers[i] = p.ID
		}
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{
				Type:  "list",
				Peers: peers,
			})
		}
	}
}

// SignalParser provides helper methods to parse incoming signals
type SignalParser struct{}

// ParseOffer parses an offer from a signal
func (p *SignalParser) ParseOffer(s *sdp.Signal) (*sdp.SessionDescription, error) {
	return sdp.ParseSessionDescription(s)
}

// ParseAnswer parses an answer from a signal
func (p *SignalParser) ParseAnswer(s *sdp.Signal) (*sdp.SessionDescription, error) {
	return sdp.ParseSessionDescription(s)
}

// ParseICECandidate parses an ICE candidate from a signal
func (p *SignalParser) ParseICECandidate(s *sdp.Signal) (*sdp.ICECandidate, error) {
	return sdp.ParseICECandidate(s)
}

// DefaultSignalParser is a ready-to-use signal parser
var DefaultSignalParser = &SignalParser{}
