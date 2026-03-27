// Package ice provides manual ICE (Interactive Connectivity Establishment)
// for UDP-based P2P connections without external dependencies.
package ice

import (
	"fmt"
	"net"
	"strconv"
	"sync"
)

// CandidateType represents the type of ICE candidate
type CandidateType string

const (
	// CandidateTypeHost is a local address (direct connection)
	CandidateTypeHost CandidateType = "host"
	// CandidateTypeServerReflexive would be STUN-discovered (not implemented)
	CandidateTypeServerReflexive CandidateType = "srflx"
	// CandidateTypePeerReflexive would be peer-discovered (not implemented)
	CandidateTypePeerReflexive CandidateType = "prflx"
	// CandidateTypeRelay would be TUN-relayed (not implemented)
	CandidateTypeRelay CandidateType = "relay"
)

// Candidate represents an ICE candidate for UDP connectivity
type Candidate struct {
	// Type is the candidate type (host, srflx, prflx, relay)
	Type CandidateType `json:"type"`

	// Foundation is an identifier for this candidate
	Foundation string `json:"foundation"`

	// Component is 1 for RTP (we only use 1 for data)
	Component int `json:"component"`

	// Protocol is always "udp" for now
	Protocol string `json:"protocol"`

	// Priority is the candidate priority (higher = preferred)
	Priority uint32 `json:"priority"`

	// IP address
	IP string `json:"ip"`

	// Port number
	Port int `json:"port"`

	// Base is the base address (same as IP:Port for host candidates)
	Base string `json:"base,omitempty"`
}

// String returns the candidate in standard format
func (c *Candidate) String() string {
	// candidate:<foundation> <component> <protocol> <priority> <ip> <port> typ <type>
	return fmt.Sprintf("candidate:%s %d %s %d %s %d typ %s",
		c.Foundation, c.Component, c.Protocol, c.Priority, c.IP, c.Port, c.Type)
}

// ParseCandidate parses a candidate string
func ParseCandidate(s string) (*Candidate, error) {
	var c Candidate
	_, err := fmt.Sscanf(s, "candidate:%s %d %s %d %s %d typ %s",
		&c.Foundation, &c.Component, &c.Protocol, &c.Priority, &c.IP, &c.Port, &c.Type)
	if err != nil {
		return nil, fmt.Errorf("parse candidate: %w", err)
	}
	return &c, nil
}

// Gatherer collects local ICE candidates
type Gatherer struct {
	mu         sync.RWMutex
	candidates []*Candidate
	listeners  []*net.UDPConn
	portStart  int
	portEnd    int
}

// GathererConfig configures the ICE gatherer
type GathererConfig struct {
	// PortStart is the beginning of the port range to use (0 = any)
	PortStart int
	// PortEnd is the end of the port range to use (0 = any)
	PortEnd int
}

// DefaultGathererConfig returns a default config
func DefaultGathererConfig() GathererConfig {
	return GathererConfig{
		PortStart: 10000,
		PortEnd:   10100,
	}
}

// NewGatherer creates a new ICE gatherer
func NewGatherer(config GathererConfig) *Gatherer {
	return &Gatherer{
		candidates: make([]*Candidate, 0),
		listeners:  make([]*net.UDPConn, 0),
		portStart:  config.PortStart,
		portEnd:    config.PortEnd,
	}
}

// Gather collects all local ICE candidates
func (g *Gatherer) Gather() ([]*Candidate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Get local IP addresses
	ips, err := g.getLocalIPs()
	if err != nil {
		return nil, fmt.Errorf("get local IPs: %w", err)
	}

	// Create a UDP listener for each IP and gather candidates
	foundation := 1
	for _, ip := range ips {
		// Try to bind a UDP socket
		conn, err := g.bindUDP(ip)
		if err != nil {
			continue // Skip this IP if we can't bind
		}

		// Get the actual local address (includes assigned port)
		localAddr := conn.LocalAddr().(*net.UDPAddr)

		// Create host candidate
		candidate := &Candidate{
			Type:       CandidateTypeHost,
			Foundation: strconv.Itoa(foundation),
			Component:  1,
			Protocol:   "udp",
			Priority:   g.calculatePriority(CandidateTypeHost, localAddr.IP),
			IP:         localAddr.IP.String(),
			Port:       localAddr.Port,
		}

		g.candidates = append(g.candidates, candidate)
		g.listeners = append(g.listeners, conn)
		foundation++
	}

	if len(g.candidates) == 0 {
		return nil, fmt.Errorf("no local candidates gathered")
	}

	return g.candidates, nil
}

// GetCandidates returns the gathered candidates
func (g *Gatherer) GetCandidates() []*Candidate {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]*Candidate, len(g.candidates))
	copy(result, g.candidates)
	return result
}

// GetListeners returns the UDP listeners (for direct communication)
func (g *Gatherer) GetListeners() []*net.UDPConn {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]*net.UDPConn, len(g.listeners))
	copy(result, g.listeners)
	return result
}

// Close closes all UDP listeners
func (g *Gatherer) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, conn := range g.listeners {
		conn.Close()
	}
	g.listeners = g.listeners[:0]
	return nil
}

// getLocalIPs returns all non-loopback local IP addresses
func (g *Gatherer) getLocalIPs() ([]net.IP, error) {
	var ips []net.IP

	// Get all network interfaces
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("get interfaces: %w", err)
	}

	for _, iface := range ifaces {
		// Skip down interfaces and loopback
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		// Get addresses for this interface
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			// Extract IP from address
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Only use IPv4 for now (IPv6 can be added later)
			if ip != nil && ip.To4() != nil {
				ips = append(ips, ip)
			}
		}
	}

	// Fallback to localhost if no external IPs found (for testing)
	if len(ips) == 0 {
		ips = append(ips, net.ParseIP("127.0.0.1"))
	}

	return ips, nil
}

// bindUDP creates a UDP socket bound to the given IP
func (g *Gatherer) bindUDP(ip net.IP) (*net.UDPConn, error) {
	// If port range is specified, try each port
	if g.portStart > 0 && g.portEnd > 0 {
		for port := g.portStart; port <= g.portEnd; port++ {
			addr := &net.UDPAddr{IP: ip, Port: port}
			conn, err := net.ListenUDP("udp", addr)
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("no available ports in range %d-%d", g.portStart, g.portEnd)
	}

	// Let system assign a port
	addr := &net.UDPAddr{IP: ip, Port: 0}
	return net.ListenUDP("udp", addr)
}

// calculatePriority calculates candidate priority (RFC 5245)
func (g *Gatherer) calculatePriority(typ CandidateType, ip net.IP) uint32 {
	// Priority = (2^24)*(type preference) + (2^8)*(local preference) + (256 - component ID)
	
	typePref := uint32(0)
	switch typ {
	case CandidateTypeHost:
		typePref = 126
	case CandidateTypePeerReflexive:
		typePref = 110
	case CandidateTypeServerReflexive:
		typePref = 100
	case CandidateTypeRelay:
		typePref = 0
	}

	// Local preference: prefer non-private IPs slightly
	localPref := uint32(0)
	if ip.IsPrivate() {
		localPref = 100
	} else {
		localPref = 200
	}

	return (1 << 24) * typePref + (1 << 8) * localPref + (256 - 1)
}

// CandidatePair represents a local+remote candidate pair for connectivity checks
type CandidatePair struct {
	Local  *Candidate
	Remote *Candidate
	// State tracks the pair state (frozen, waiting, in-progress, succeeded, failed)
	State string
}

// String returns a readable representation
func (p *CandidatePair) String() string {
	return fmt.Sprintf("%s <-> %s:%d (%s)", p.Local.IP, p.Remote.IP, p.Remote.Port, p.State)
}
