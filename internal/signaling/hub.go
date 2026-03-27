package signaling

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Hub manages all peer connections and message routing
type Hub struct {
	peers    map[string]*Peer
	register chan *Peer
	unregister chan *Peer
	broadcast  chan Message
	mu         sync.RWMutex
	logger     *slog.Logger
}

// Peer represents a connected client
type Peer struct {
	ID       string
	Send     chan Message
	Hub      *Hub
	JoinedAt time.Time
}

// NewHub creates a new signaling hub
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		peers:      make(map[string]*Peer),
		register:   make(chan *Peer),
		unregister: make(chan *Peer),
		broadcast:  make(chan Message),
		logger:     logger,
	}
}

// Run starts the hub's event loop
func (h *Hub) Run() {
	h.logger.Info("Hub started")
	for {
		select {
		case peer := <-h.register:
			h.handleRegister(peer)
		case peer := <-h.unregister:
			h.handleUnregister(peer)
		case msg := <-h.broadcast:
			h.handleBroadcast(msg)
		}
	}
}

func (h *Hub) handleRegister(peer *Peer) {
	h.mu.Lock()
	h.peers[peer.ID] = peer
	h.mu.Unlock()

	h.logger.Info("Peer registered", "peer_id", peer.ID, "total_peers", len(h.peers))

	// Send peer their ID
	regData := PeerRegisteredData{PeerID: peer.ID}
	regMsg := Message{
		Type:      PeerRegistered,
		Data:      mustMarshal(regData),
		Timestamp: time.Now().Unix(),
	}
	peer.Send <- regMsg

	// Send peer list to the new peer
	h.sendPeerList(peer)

	// Notify other peers about the new peer
	joinedData := PeerJoinedLeftData{
		Peer: PeerInfo{ID: peer.ID, JoinedAt: peer.JoinedAt.Unix()},
	}
	joinedMsg := Message{
		Type:      PeerJoined,
		Data:      mustMarshal(joinedData),
		Timestamp: time.Now().Unix(),
	}
	h.broadcastToOthers(joinedMsg, peer.ID)
}

func (h *Hub) handleUnregister(peer *Peer) {
	h.mu.Lock()
	if _, ok := h.peers[peer.ID]; ok {
		delete(h.peers, peer.ID)
		close(peer.Send)
		h.logger.Info("Peer unregistered", "peer_id", peer.ID, "total_peers", len(h.peers))
	}
	h.mu.Unlock()

	// Notify remaining peers
	leftData := PeerJoinedLeftData{
		Peer: PeerInfo{ID: peer.ID},
	}
	leftMsg := Message{
		Type:      PeerLeft,
		Data:      mustMarshal(leftData),
		Timestamp: time.Now().Unix(),
	}
	h.broadcastToAll(leftMsg)
}

func (h *Hub) handleBroadcast(msg Message) {
	// If message has a specific target, send only to that peer
	if msg.To != "" {
		h.mu.RLock()
		target, ok := h.peers[msg.To]
		h.mu.RUnlock()
		if ok {
			select {
			case target.Send <- msg:
				h.logger.Debug("Message forwarded", "from", msg.From, "to", msg.To, "type", msg.Type)
			default:
				h.logger.Warn("Failed to send to peer, channel full", "peer_id", msg.To)
			}
		} else {
			h.logger.Warn("Target peer not found", "peer_id", msg.To)
			// Send error back to sender
			h.mu.RLock()
			sender, ok := h.peers[msg.From]
			h.mu.RUnlock()
			if ok {
				errData := ErrorData{
					Message: "Target peer not found: " + msg.To,
					Code:    "PEER_NOT_FOUND",
				}
				errMsg := Message{
					Type:      Error,
					To:        msg.From,
					Data:      mustMarshal(errData),
					Timestamp: time.Now().Unix(),
				}
				sender.Send <- errMsg
			}
		}
		return
	}

	// Otherwise broadcast to all peers
	h.broadcastToAll(msg)
}

func (h *Hub) sendPeerList(peer *Peer) {
	h.mu.RLock()
	peers := make([]PeerInfo, 0, len(h.peers))
	for id, p := range h.peers {
		if id != peer.ID { // Don't include the requesting peer
			peers = append(peers, PeerInfo{ID: id, JoinedAt: p.JoinedAt.Unix()})
		}
	}
	h.mu.RUnlock()

	listData := PeerListData{Peers: peers}
	listMsg := Message{
		Type:      PeerList,
		Data:      mustMarshal(listData),
		Timestamp: time.Now().Unix(),
	}
	peer.Send <- listMsg
}

func (h *Hub) broadcastToAll(msg Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, peer := range h.peers {
		select {
		case peer.Send <- msg:
		default:
			h.logger.Warn("Failed to broadcast to peer, channel full", "peer_id", id)
		}
	}
}

func (h *Hub) broadcastToOthers(msg Message, excludeID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, peer := range h.peers {
		if id == excludeID {
			continue
		}
		select {
		case peer.Send <- msg:
		default:
			h.logger.Warn("Failed to broadcast to peer, channel full", "peer_id", id)
		}
	}
}

// GetRegisterChan returns the channel for registering peers
func (h *Hub) GetRegisterChan() chan<- *Peer {
	return h.register
}

// GetUnregisterChan returns the channel for unregistering peers
func (h *Hub) GetUnregisterChan() chan<- *Peer {
	return h.unregister
}

// GetBroadcastChan returns the channel for broadcasting messages
func (h *Hub) GetBroadcastChan() chan<- Message {
	return h.broadcast
}

func mustMarshal(v interface{}) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}
