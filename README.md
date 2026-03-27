# P2P Hub

A minimal P2P signaling server and client library in Go. Provides WebSocket-based signaling for establishing peer-to-peer connections.

## Architecture

```
┌─────────────┐      WebSocket       ┌─────────────┐
│   Client A  │◄────────────────────►│   Server    │
│  (p2p lib)  │                      │   (hub)     │
└──────┬──────┘                      └──────┬──────┘
       │                                     │
       │    Signaling (SDP/ICE exchange)     │
       │◄───────────────────────────────────►│
       │                                     │
┌──────┴──────┐                      ┌──────┴──────┐
│   Client B  │◄────────────────────►│  WebSocket  │
│  (p2p lib)  │                      │   handler   │
└─────────────┘                      └─────────────┘
```

## Quick Start

### Start the Server

```bash
./bin/server -addr=:8080 -log-level=debug
```

### Use the Client Library

```go
package main

import (
    "log"
    "p2phub/internal/p2p"
    "p2phub/internal/p2p/sdp"
)

func main() {
    // Connect to signaling server
    client, err := p2p.Connect("ws://localhost:8080/ws", nil)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Handle incoming signals
    client.OnSignal(func(from string, sig *sdp.Signal) {
        switch sig.SignalType {
        case sdp.TypeOffer:
            offer, _ := sdp.ParseSessionDescription(sig)
            log.Printf("Received offer from %s: %s", from, offer)
            
            // Send answer
            answer := sdp.NewAnswer("v=0\r\n...")
            client.SendAnswer(from, answer)
            
        case sdp.TypeAnswer:
            answer, _ := sdp.ParseSessionDescription(sig)
            log.Printf("Received answer from %s: %s", from, answer)
            
        case sdp.TypeICE:
            candidate, _ := sdp.ParseICECandidate(sig)
            log.Printf("Received ICE candidate: %s", candidate.Candidate)
        }
    })

    // Handle peer events
    client.OnPeerEvent(func(event p2p.PeerEvent) {
        switch event.Type {
        case "joined":
            log.Printf("Peer joined: %s", event.PeerID)
            // Send them an offer
            offer := sdp.NewOffer("v=0\r\n...")
            client.SendOffer(event.PeerID, offer)
        case "left":
            log.Printf("Peer left: %s", event.PeerID)
        }
    })

    // Keep running...
    select {}
}
```

## Library Structure

```
internal/p2p/
├── sdp/              # SDP types and parsing
│   └── sdp.go        # SessionDescription, ICECandidate, Signal
├── transport/        # WebSocket transport
│   └── websocket.go  # WebSocketClient, message handling
└── client.go         # High-level P2P client API
```

### SDP Package (`internal/p2p/sdp`)

Core types for WebRTC-style signaling:

- **`SessionDescription`** - SDP offer or answer
- **`ICECandidate`** - NAT traversal candidate
- **`Signal`** - Envelope for all signaling messages

```go
// Create an offer
offer := sdp.NewOffer("v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\n...")

// Convert to signal for sending
sig, _ := offer.ToSignal()

// Parse received signal
offer, err := sdp.ParseSessionDescription(sig)
```

### Transport Package (`internal/p2p/transport`)

Low-level WebSocket client:

```go
config := transport.DefaultConfig("ws://localhost:8080/ws")
client := transport.NewWebSocketClient(config)

client.Connect()
client.Send(toPeerID, data)
client.SetHandler(handler)
```

### High-Level Client (`internal/p2p`)

Simplified API:

```go
client, _ := p2p.Connect(serverURL, &p2p.Options{Logger: logger})

client.SendOffer(peerID, offer)
client.SendAnswer(peerID, answer)
client.SendICECandidate(peerID, candidate)

client.OnSignal(handler)
client.OnPeerEvent(handler)

peers := client.Peers()
myID := client.MyID()
```

## Running the Demo

```bash
# Terminal 1: Start server
./bin/server

# Terminal 2: Run demo
./scripts/demo_parallel.sh
```

## Protocol

### Signaling Message Format

```json
{
  "type": "signal",
  "from": "peer-id-1",
  "to": "peer-id-2",
  "data": {
    "signal_type": "offer",
    "payload": "{\"type\":\"offer\",\"sdp\":\"v=0...\"}",
    "timestamp": "2024-01-01T00:00:00Z"
  },
  "timestamp": 1704067200
}
```

### Server Message Types

- `peer_registered` - Sent to new peer with their ID
- `peer_list` - List of available peers
- `peer_joined` - New peer connected
- `peer_left` - Peer disconnected
- `signal` - Forwarded signal from another peer
- `error` - Error response

## Building

```bash
go build -o bin/server ./cmd/server
go build -o bin/client ./cmd/client
```

## Future Work

- [ ] UDP hole punching for direct P2P
- [ ] STUN/TURN server integration
- [ ] DTLS handshake
- [ ] Data channel implementation
- [ ] Connection state management
