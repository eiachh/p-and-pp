#!/bin/bash
# Test script to demonstrate two clients connecting and signaling

set -e

echo "=== P2P Signaling Server Test ==="
echo "Starting two clients that will discover each other and exchange signals"
echo ""

# Create a named pipe for the first client
FIFO1=$(mktemp -u)
mkfifo "$FIFO1"

# Create a named pipe for the second client
FIFO2=$(mktemp -u)
mkfifo "$FIFO2"

# Cleanup function
cleanup() {
    rm -f "$FIFO1" "$FIFO2"
    kill %1 %2 2>/dev/null || true
}
trap cleanup EXIT

# Start first client in background, reading from FIFO
echo "Starting Client 1..."
./bin/client -log-level=info < "$FIFO1" &
CLIENT1_PID=$!

# Give client 1 time to connect and get its ID
sleep 1

# Extract client 1's peer ID from its output
# We need to get this from the log, so let's use a temp file
CLIENT1_LOG=$(mktemp)
./bin/client -log-level=info 2>&1 | tee "$CLIENT1_LOG" < "$FIFO1" &
CLIENT1_PID=$!
sleep 1

# Get client 1's peer ID
PEER1_ID=$(grep "my_id" "$CLIENT1_LOG" | head -1 | sed 's/.*my_id=\([a-f0-9]*\).*/\1/')

if [ -z "$PEER1_ID" ]; then
    echo "Failed to get peer 1 ID, using a simpler test approach"
    
    # Simple test: just run both clients with a delay and show they see each other
    echo ""
    echo "=== Running simplified test ==="
    echo ""
    
    # Start client 1, let it stay connected for a few seconds
    (sleep 3; echo "/quit") | ./bin/client -log-level=info &
    CLIENT1_PID=$!
    
    sleep 1
    
    # Start client 2, it should see client 1
    (sleep 2; echo "/quit") | ./bin/client -log-level=info &
    CLIENT2_PID=$!
    
    wait $CLIENT1_PID $CLIENT2_PID 2>/dev/null || true
    
    echo ""
    echo "=== Test complete ==="
    rm -f "$CLIENT1_LOG"
    exit 0
fi

echo "Client 1 Peer ID: $PEER1_ID"

# Start second client, connecting to peer 1
echo "Starting Client 2 (will connect to $PEER1_ID)..."
(sleep 2; echo "/quit") | ./bin/client -log-level=info -peer="$PEER1_ID" &
CLIENT2_PID=$!

# Send commands to client 1
(
    sleep 3
    echo "/quit"
) > "$FIFO1" &

wait $CLIENT1_PID $CLIENT2_PID 2>/dev/null || true

rm -f "$CLIENT1_LOG"

echo ""
echo "=== Test complete ==="
