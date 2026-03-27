// Package transport provides WebSocket transport for P2P signaling.
package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Message represents a message from/to the signaling server
type Message struct {
	Type      string          `json:"type"`
	From      string          `json:"from,omitempty"`
	To        string          `json:"to,omitempty"`
	Data      json.RawMessage `json:"data"`
	Timestamp int64           `json:"timestamp"`
}

// PeerInfo represents information about a peer
type PeerInfo struct {
	ID       string `json:"id"`
	JoinedAt int64  `json:"joined_at"`
}

// MessageType constants for server messages
const (
	MsgPeerRegistered = "peer_registered"
	MsgPeerList       = "peer_list"
	MsgPeerJoined     = "peer_joined"
	MsgPeerLeft       = "peer_left"
	MsgSignal         = "signal"
	MsgError          = "error"
)

// Handler is called when a message is received
type Handler func(msg Message)

// PeerHandler is called when peer events occur
type PeerHandler func(event PeerEvent)

// PeerEvent represents a peer-related event
type PeerEvent struct {
	Type   string   // "joined", "left", "list"
	Peer   PeerInfo // For joined/left events
	Peers  []PeerInfo // For list events
	MyID   string   // For registered event
}

// Config for WebSocket connection
type Config struct {
	ServerURL      string
	Logger         *slog.Logger
	ReconnectDelay time.Duration
	PingInterval   time.Duration
}

// DefaultConfig returns a default configuration
func DefaultConfig(serverURL string) Config {
	return Config{
		ServerURL:      serverURL,
		Logger:         slog.Default(),
		ReconnectDelay: 5 * time.Second,
		PingInterval:   30 * time.Second,
	}
}

// WebSocketClient manages the connection to the signaling server
type WebSocketClient struct {
	config      Config
	conn        *websocket.Conn
	mu          sync.RWMutex
	writeMu     sync.Mutex // Protects WebSocket writes (not safe for concurrent use)
	handler     Handler
	peerHandler PeerHandler
	myID        string
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewWebSocketClient creates a new WebSocket client
func NewWebSocketClient(config Config) *WebSocketClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &WebSocketClient{
		config: config,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
}

// Connect establishes the WebSocket connection
func (c *WebSocketClient) Connect() error {
	u, err := url.Parse(c.config.ServerURL)
	if err != nil {
		return fmt.Errorf("parse server URL: %w", err)
	}

	c.config.Logger.Info("Connecting to signaling server", "url", u.String())

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// Start read/write pumps
	go c.readPump()
	go c.writePump()

	return nil
}

// Close closes the connection
func (c *WebSocketClient) Close() error {
	c.cancel()
	
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	
	<-c.done
	return nil
}

// Send sends a message to a specific peer
func (c *WebSocketClient) Send(to string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	msg := Message{
		Type:      MsgSignal,
		To:        to,
		Data:      payload,
		Timestamp: time.Now().Unix(),
	}

	return c.sendMessage(msg)
}

// Broadcast sends a message to all peers
func (c *WebSocketClient) Broadcast(data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	msg := Message{
		Type:      MsgSignal,
		Data:      payload,
		Timestamp: time.Now().Unix(),
	}

	return c.sendMessage(msg)
}

// MyID returns this client's peer ID (available after registration)
func (c *WebSocketClient) MyID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.myID
}

// Logger returns the logger
func (c *WebSocketClient) Logger() *slog.Logger {
	return c.config.Logger
}

// SetHandler sets the message handler
func (c *WebSocketClient) SetHandler(h Handler) {
	c.handler = h
}

// SetPeerHandler sets the peer event handler
func (c *WebSocketClient) SetPeerHandler(h PeerHandler) {
	c.peerHandler = h
}

func (c *WebSocketClient) sendMessage(msg Message) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	// Serialize writes - WebSocket is not safe for concurrent writes
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	// Set write deadline to prevent blocking forever
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetWriteDeadline(time.Time{}) // Clear deadline after write

	return conn.WriteMessage(websocket.TextMessage, data)
}

func (c *WebSocketClient) readPump() {
	defer close(c.done)

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.config.Logger.Warn("WebSocket read error", "error", err)
			}
			return
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			c.config.Logger.Warn("Failed to unmarshal message", "error", err)
			continue
		}

		c.handleMessage(msg)
	}
}

func (c *WebSocketClient) writePump() {
	ticker := time.NewTicker(c.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()
			
			if conn != nil {
				c.writeMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				c.writeMu.Unlock()
				
				if err != nil {
					c.config.Logger.Warn("Ping failed", "error", err)
					return
				}
			}
		}
	}
}

func (c *WebSocketClient) handleMessage(msg Message) {
	switch msg.Type {
	case MsgPeerRegistered:
		var data struct {
			PeerID string `json:"peer_id"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.config.Logger.Error("Failed to unmarshal peer_registered", "error", err)
			return
		}
		c.mu.Lock()
		c.myID = data.PeerID
		c.mu.Unlock()
		c.config.Logger.Info("Registered with server", "peer_id", data.PeerID)
		
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{Type: "registered", MyID: data.PeerID})
		}

	case MsgPeerList:
		var data struct {
			Peers []PeerInfo `json:"peers"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.config.Logger.Error("Failed to unmarshal peer_list", "error", err)
			return
		}
		c.config.Logger.Info("Received peer list", "count", len(data.Peers))
		
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{Type: "list", Peers: data.Peers})
		}

	case MsgPeerJoined:
		var data struct {
			Peer PeerInfo `json:"peer"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.config.Logger.Error("Failed to unmarshal peer_joined", "error", err)
			return
		}
		c.config.Logger.Info("Peer joined", "peer_id", data.Peer.ID)
		
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{Type: "joined", Peer: data.Peer})
		}

	case MsgPeerLeft:
		var data struct {
			Peer PeerInfo `json:"peer"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.config.Logger.Error("Failed to unmarshal peer_left", "error", err)
			return
		}
		c.config.Logger.Info("Peer left", "peer_id", data.Peer.ID)
		
		if c.peerHandler != nil {
			c.peerHandler(PeerEvent{Type: "left", Peer: data.Peer})
		}

	case MsgSignal:
		if c.handler != nil {
			c.handler(msg)
		}

	case MsgError:
		var data struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			c.config.Logger.Error("Failed to unmarshal error", "error", err)
			return
		}
		c.config.Logger.Error("Server error", "code", data.Code, "message", data.Message)

	default:
		c.config.Logger.Debug("Unknown message type", "type", msg.Type)
	}
}
