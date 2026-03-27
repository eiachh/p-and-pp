package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"p2phub/internal/signaling"

	"github.com/gorilla/websocket"
)

var (
	addr     = flag.String("addr", ":8080", "WebSocket server address")
	logLevel = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for development
		return true
	},
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func main() {
	flag.Parse()

	// Setup logger
	level := parseLogLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	logger.Info("Starting P2P Signaling Server", "address", *addr)

	// Create and start the hub
	hub := signaling.NewHub(logger)
	go hub.Run()

	// Setup HTTP handlers
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(hub, w, r, logger)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Create server
	server := &http.Server{
		Addr:    *addr,
		Handler: nil,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	logger.Info("Server started", "address", *addr)

	<-ctx.Done()
	logger.Info("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", "error", err)
	}

	logger.Info("Server stopped")
}

func handleWebSocket(hub *signaling.Hub, w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("WebSocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// Generate unique peer ID
	peerID := generatePeerID()
	logger.Info("New WebSocket connection", "peer_id", peerID, "remote_addr", r.RemoteAddr)

	// Create peer
	peer := &signaling.Peer{
		ID:       peerID,
		Send:     make(chan signaling.Message, 256),
		Hub:      hub,
		JoinedAt: time.Now(),
	}

	// Register with hub
	hub.GetRegisterChan() <- peer

	// Start goroutines for reading and writing
	done := make(chan struct{})
	go writePump(peer, conn, done, logger)
	go readPump(peer, conn, hub, done, logger)

	// Wait for done signal
	<-done

	// Unregister from hub
	hub.GetUnregisterChan() <- peer
	logger.Info("Peer disconnected", "peer_id", peerID)
}

func readPump(peer *signaling.Peer, conn *websocket.Conn, hub *signaling.Hub, done chan<- struct{}, logger *slog.Logger) {
	defer func() {
		done <- struct{}{}
	}()

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Warn("WebSocket read error", "peer_id", peer.ID, "error", err)
			}
			return
		}

		// Parse the message
		var msg signaling.Message
		if err := json.Unmarshal(message, &msg); err != nil {
			logger.Warn("Failed to parse message", "peer_id", peer.ID, "error", err)
			continue
		}

		// Set the From field to ensure it's correct
		msg.From = peer.ID
		msg.Timestamp = time.Now().Unix()

		logger.Debug("Received message", "peer_id", peer.ID, "type", msg.Type, "to", msg.To)

		// Send to hub for routing
		hub.GetBroadcastChan() <- msg
	}
}

func writePump(peer *signaling.Peer, conn *websocket.Conn, done chan<- struct{}, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		done <- struct{}{}
		conn.Close()
	}()

	for {
		select {
		case message, ok := <-peer.Send:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			data, err := json.Marshal(message)
			if err != nil {
				logger.Error("Failed to marshal message", "error", err)
				continue
			}

			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				logger.Warn("WebSocket write error", "peer_id", peer.ID, "error", err)
				return
			}

			logger.Debug("Sent message", "peer_id", peer.ID, "type", message.Type)

		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func generatePeerID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
