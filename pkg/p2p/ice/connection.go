package ice

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// ReceiveHandler is called when data is received on the connection
type ReceiveHandler func(data []byte, from *net.UDPAddr)

// Connection represents a P2P connection established via ICE
type Connection struct {
	mu sync.RWMutex

	// Local and remote candidates that were selected
	localCandidate  *Candidate
	remoteCandidate *Candidate

	// The UDP connection used for communication
	conn *net.UDPConn

	// Remote address for sending
	remoteAddr *net.UDPAddr

	// Logger
	logger *slog.Logger

	// Closed flag
	closed bool

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Receive handler callback
	receiveHandler ReceiveHandler
}

// ConnectionConfig configures a P2P connection
type ConnectionConfig struct {
	Logger *slog.Logger
}

// NewConnection creates a new ICE connection from a selected candidate pair
func NewConnection(local *Candidate, remote *Candidate, localConn *net.UDPConn, config ConnectionConfig) (*Connection, error) {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Parse remote address
	remoteAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", remote.IP, remote.Port))
	if err != nil {
		return nil, fmt.Errorf("resolve remote address: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Connection{
		localCandidate:  local,
		remoteCandidate: remote,
		conn:            localConn,
		remoteAddr:      remoteAddr,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
	}

	// Start receiving data
	go c.receiveLoop()

	logger.Info("ICE connection established",
		"local", fmt.Sprintf("%s:%d", local.IP, local.Port),
		"remote", fmt.Sprintf("%s:%d", remote.IP, remote.Port))

	return c, nil
}

// Send sends data to the remote peer
func (c *Connection) Send(data []byte) error {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return fmt.Errorf("connection closed")
	}
	conn := c.conn
	remoteAddr := c.remoteAddr
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("no connection")
	}

	_, err := conn.WriteToUDP(data, remoteAddr)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}

	return nil
}

// SendString sends a string message
func (c *Connection) SendString(msg string) error {
	return c.Send([]byte(msg))
}

// Receive receives data (blocking)
func (c *Connection) Receive(buffer []byte) (int, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return 0, fmt.Errorf("connection closed")
	}
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return 0, fmt.Errorf("no connection")
	}

	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return 0, err
	}

	// Only accept data from our remote peer
	if addr.String() != c.remoteAddr.String() {
		return 0, fmt.Errorf("unexpected sender: %s", addr)
	}

	return n, nil
}

// Close closes the connection
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	c.cancel()

	c.logger.Info("ICE connection closed")
	return nil
}

// LocalCandidate returns the local candidate
func (c *Connection) LocalCandidate() *Candidate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.localCandidate
}

// RemoteCandidate returns the remote candidate
func (c *Connection) RemoteCandidate() *Candidate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteCandidate
}

// SetReceiveHandler sets the handler for received data
func (c *Connection) SetReceiveHandler(handler ReceiveHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.receiveHandler = handler
}

// receiveLoop handles incoming data
func (c *Connection) receiveLoop() {
	buffer := make([]byte, 65535)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		handler := c.receiveHandler
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		// Set read timeout to allow checking context
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			// Connection error
			c.logger.Warn("Receive error", "error", err)
			return
		}

		// Check if it's from our peer
		if addr.String() != c.remoteAddr.String() {
			c.logger.Debug("Received data from unexpected source", "from", addr)
			continue
		}

		// Make a copy of the data
		data := make([]byte, n)
		copy(data, buffer[:n])

		// Log at INFO level
		c.logger.Info("Received UDP data", "bytes", n, "from", addr, "data", string(data))

		// Call handler if set
		if handler != nil {
			handler(data, addr)
		}
	}
}

// Agent manages the ICE gathering and connection process
type Agent struct {
	mu sync.RWMutex

	// Gatherer for local candidates
	gatherer *Gatherer

	// Local candidates
	localCandidates []*Candidate

	// Remote candidates received from peer
	remoteCandidates []*Candidate

	// Selected connection pair
	connection *Connection

	// Configuration
	config AgentConfig

	// Logger
	logger *slog.Logger
}

// AgentConfig configures the ICE agent
type AgentConfig struct {
	GathererConfig GathererConfig
	Logger         *slog.Logger
}

// NewAgent creates a new ICE agent
func NewAgent(config AgentConfig) *Agent {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Agent{
		gatherer:         NewGatherer(config.GathererConfig),
		localCandidates:  make([]*Candidate, 0),
		remoteCandidates: make([]*Candidate, 0),
		config:           config,
		logger:           logger,
	}
}

// Gather gathers local candidates
func (a *Agent) Gather() ([]*Candidate, error) {
	candidates, err := a.gatherer.Gather()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.localCandidates = candidates
	a.mu.Unlock()

	a.logger.Info("ICE gathering complete", "candidates", len(candidates))
	for _, c := range candidates {
		a.logger.Info("  Local candidate", "ip", c.IP, "port", c.Port, "type", c.Type)
	}

	return candidates, nil
}

// AddRemoteCandidate adds a remote candidate received from the peer
func (a *Agent) AddRemoteCandidate(candidate *Candidate) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.remoteCandidates = append(a.remoteCandidates, candidate)
	a.logger.Info("Added remote candidate", "ip", candidate.IP, "port", candidate.Port)
}

// GetLocalCandidates returns the local candidates
func (a *Agent) GetLocalCandidates() []*Candidate {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]*Candidate, len(a.localCandidates))
	copy(result, a.localCandidates)
	return result
}

// GetRemoteCandidates returns the remote candidates
func (a *Agent) GetRemoteCandidates() []*Candidate {
	a.mu.RLock()
	defer a.mu.Unlock()

	result := make([]*Candidate, len(a.remoteCandidates))
	copy(result, a.remoteCandidates)
	return result
}

// Connect establishes a connection using the best candidate pair
// For simplicity, we just use the first matching pair
func (a *Agent) Connect() (*Connection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.localCandidates) == 0 {
		return nil, fmt.Errorf("no local candidates")
	}
	if len(a.remoteCandidates) == 0 {
		return nil, fmt.Errorf("no remote candidates")
	}

	// Get listeners from gatherer
	listeners := a.gatherer.GetListeners()

	// Simple strategy: use the first local candidate with the first remote candidate
	// In a real implementation, we'd do connectivity checks
	local := a.localCandidates[0]

	// Find the listener for this local candidate
	var localConn *net.UDPConn
	for i, c := range a.localCandidates {
		if c == local && i < len(listeners) {
			localConn = listeners[i]
			break
		}
	}

	if localConn == nil {
		return nil, fmt.Errorf("no listener for local candidate")
	}

	// Use the first remote candidate
	remote := a.remoteCandidates[0]

	conn, err := NewConnection(local, remote, localConn, ConnectionConfig{Logger: a.logger})
	if err != nil {
		return nil, fmt.Errorf("create connection: %w", err)
	}

	a.connection = conn
	return conn, nil
}

// GetConnection returns the established connection
func (a *Agent) GetConnection() *Connection {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connection
}

// Close closes the agent and all resources
func (a *Agent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.connection != nil {
		a.connection.Close()
	}

	if a.gatherer != nil {
		a.gatherer.Close()
	}

	return nil
}

// CandidateToJSON serializes a candidate to JSON
func CandidateToJSON(c *Candidate) ([]byte, error) {
	return json.Marshal(c)
}

// CandidateFromJSON deserializes a candidate from JSON
func CandidateFromJSON(data []byte) (*Candidate, error) {
	var c Candidate
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
