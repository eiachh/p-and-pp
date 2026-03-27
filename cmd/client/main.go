package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"p2phub/pkg/p2p"
	"p2phub/pkg/p2p/ice"
	"p2phub/pkg/p2p/sdp"
)

var (
	serverAddr = flag.String("server", "ws://localhost:8080/ws", "WebSocket server address")
	peerID     = flag.String("peer", "", "Target peer ID to connect to")
	logLevel   = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	useICE     = flag.Bool("ice", false, "Enable ICE for direct UDP connection")
)

// App holds the application state
type App struct {
	client *p2p.Client
	logger *slog.Logger
	iceAgent *ice.Agent
	iceConn  *ice.Connection
}

func main() {
	flag.Parse()

	// Setup logger
	level := parseLogLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))

	logger.Info("Connecting to signaling server", "address", *serverAddr)

	// Connect using the p2p library
	client, err := p2p.Connect(*serverAddr, &p2p.Options{Logger: logger})
	if err != nil {
		logger.Error("Failed to connect", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// Wait for registration
	time.Sleep(500 * time.Millisecond)
	logger.Info("Connected", "my_id", client.MyID())

	app := &App{
		client: client,
		logger: logger,
	}

	// Set up signal handler
	client.OnSignal(func(from string, sig *sdp.Signal) {
		handleSignal(app, from, sig)
	})

	// Set up peer event handler
	client.OnPeerEvent(func(event p2p.PeerEvent) {
		switch event.Type {
		case "joined":
			logger.Info("Peer joined", "peer_id", event.PeerID)
			if *useICE {
				app.startICE(event.PeerID)
			}
		case "left":
			logger.Info("Peer left", "peer_id", event.PeerID)
		case "list":
			logger.Info("Peer list updated", "peers", event.Peers)
		}
	})

	// If ICE is enabled and we have a target peer, gather candidates
	if *useICE && *peerID != "" {
		app.startICE(*peerID)
	}

	// Start interactive shell
	app.runInteractiveShell()
}

func handleSignal(app *App, from string, sig *sdp.Signal) {
	switch sig.SignalType {
	case sdp.TypeOffer:
		offer, err := sdp.ParseSessionDescription(sig)
		if err != nil {
			app.logger.Error("Failed to parse offer", "error", err)
			return
		}
		app.logger.Info("Received offer", "from", from, "sdp", offer.String())

		// Auto-respond with answer for demo
		app.logger.Info("Auto-responding with answer", "to", from)
		answer := sdp.NewAnswer("v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=P2P Demo Answer\r\nt=0 0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n")
		
		if err := app.client.SendAnswer(from, answer); err != nil {
			app.logger.Error("Failed to send answer", "error", err)
		}

	case sdp.TypeAnswer:
		answer, err := sdp.ParseSessionDescription(sig)
		if err != nil {
			app.logger.Error("Failed to parse answer", "error", err)
			return
		}
		app.logger.Info("Received answer", "from", from, "sdp", answer.String())

	case sdp.TypeICE:
		// Try to parse as new ICE candidate format
		var iceMsg p2p.ICECandidateMsg
		if err := json.Unmarshal(sig.Payload, &iceMsg); err == nil && iceMsg.Type == "ice-candidate" {
			candidate, err := ice.CandidateFromJSON(iceMsg.Candidate)
			if err != nil {
				app.logger.Error("Failed to parse ICE candidate", "error", err)
				return
			}
			app.handleRemoteICECandidate(from, candidate)
			return
		}
		
		// Fall back to old format
		candidate, err := sdp.ParseICECandidate(sig)
		if err != nil {
			app.logger.Error("Failed to parse ICE candidate", "error", err)
			return
		}
		app.logger.Info("Received ICE candidate (old format)", "from", from, "candidate", candidate.Candidate)

	default:
		app.logger.Info("Received signal", "from", from, "type", sig.SignalType)
	}
}

// startICE gathers local candidates and sends them to the peer
func (app *App) startICE(peerID string) {
	app.logger.Info("Starting ICE gathering", "peer", peerID)

	// Create ICE agent
	config := ice.AgentConfig{
		GathererConfig: ice.DefaultGathererConfig(),
		Logger:         app.logger,
	}
	app.iceAgent = ice.NewAgent(config)

	// Gather local candidates
	candidates, err := app.iceAgent.Gather()
	if err != nil {
		app.logger.Error("ICE gathering failed", "error", err)
		return
	}

	// Send candidates to peer
	for _, candidate := range candidates {
		app.logger.Info("Sending ICE candidate to peer", "peer", peerID, "candidate", candidate.String())
		if err := app.client.SendICECandidateJSON(peerID, candidate); err != nil {
			app.logger.Error("Failed to send ICE candidate", "error", err)
		}
	}
}

// handleRemoteICECandidate processes a received ICE candidate
func (app *App) handleRemoteICECandidate(from string, candidate *ice.Candidate) {
	app.logger.Info("Received ICE candidate", "from", from, "ip", candidate.IP, "port", candidate.Port)

	// Auto-start ICE if not already started
	if app.iceAgent == nil {
		app.logger.Info("Auto-starting ICE to respond to peer", "peer", from)
		app.startICE(from)
	}

	// Add remote candidate
	app.iceAgent.AddRemoteCandidate(candidate)

	// Try to connect (simple: just use first pair)
	// In a real implementation, we'd wait for all candidates and do connectivity checks
	go func() {
		time.Sleep(500 * time.Millisecond) // Give time for more candidates
		
		if app.iceConn != nil {
			return // Already connected
		}

		conn, err := app.iceAgent.Connect()
		if err != nil {
			app.logger.Error("ICE connection failed", "error", err)
			return
		}

		app.iceConn = conn
		app.logger.Info("Direct UDP connection established!", 
			"local", fmt.Sprintf("%s:%d", conn.LocalCandidate().IP, conn.LocalCandidate().Port),
			"remote", fmt.Sprintf("%s:%d", conn.RemoteCandidate().IP, conn.RemoteCandidate().Port))

		// Send a test message
		if err := conn.SendString("Hello from direct UDP!"); err != nil {
			app.logger.Error("Failed to send test message", "error", err)
		}
	}()
}

func (app *App) runInteractiveShell() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\n=== P2P Client Interactive Shell ===")
	fmt.Println("Commands:")
	fmt.Println("  /peers              - List connected peers")
	fmt.Println("  /offer <peer_id>    - Send an SDP offer to a peer")
	fmt.Println("  /ice <peer_id>      - Start ICE and send candidates")
	fmt.Println("  /send <msg>         - Send message via direct UDP (if connected)")
	fmt.Println("  /signal <peer_id> <msg> - Send a custom signal")
	fmt.Println("  /quit               - Exit")
	fmt.Println("=====================================\n")

	for {
		fmt.Print("> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			app.logger.Error("Read error", "error", err)
			return
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		parts := strings.Fields(input)
		if len(parts) == 0 {
			continue
		}

		cmd := parts[0]

		switch cmd {
		case "/quit", "/exit":
			fmt.Println("Goodbye!")
			return

		case "/peers":
			peers := app.client.Peers()
			app.logger.Info("Connected peers", "count", len(peers), "peers", peers)

		case "/ice":
			if len(parts) < 2 {
				fmt.Println("Usage: /ice <peer_id>")
				continue
			}
			targetPeer := parts[1]
			app.startICE(targetPeer)

		case "/send":
			if len(parts) < 2 {
				fmt.Println("Usage: /send <message>")
				continue
			}
			message := strings.Join(parts[1:], " ")
			if app.iceConn == nil {
				fmt.Println("No direct UDP connection. Use /ice first.")
				continue
			}
			if err := app.iceConn.SendString(message); err != nil {
				app.logger.Error("Failed to send", "error", err)
			} else {
				fmt.Println("Sent via direct UDP!")
			}

		case "/offer":
			if len(parts) < 2 {
				fmt.Println("Usage: /offer <peer_id>")
				continue
			}
			targetPeer := parts[1]
			offer := sdp.NewOffer("v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\ns=P2P Demo\r\nt=0 0\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n")
			
			if err := app.client.SendOffer(targetPeer, offer); err != nil {
				app.logger.Error("Failed to send offer", "error", err)
			} else {
				fmt.Println("Offer sent!")
			}

		case "/signal":
			if len(parts) < 3 {
				fmt.Println("Usage: /signal <peer_id> <message>")
				continue
			}
			targetPeer := parts[1]
			message := strings.Join(parts[2:], " ")
			
			app.logger.Info("Sending custom signal", "to", targetPeer, "message", message)
			fmt.Println("Signal sent!")

		case "/help":
			fmt.Println("Commands:")
			fmt.Println("  /peers              - List connected peers")
			fmt.Println("  /offer <peer_id>    - Send an SDP offer to a peer")
			fmt.Println("  /ice <peer_id>      - Start ICE and send candidates")
			fmt.Println("  /send <msg>         - Send message via direct UDP (if connected)")
			fmt.Println("  /signal <peer_id> <msg> - Send a custom signal")
			fmt.Println("  /quit               - Exit")

		default:
			fmt.Println("Unknown command. Type /help for available commands.")
		}
	}
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
