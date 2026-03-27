#!/bin/bash
# Parallel demo showing two clients discovering each other

echo "=========================================="
echo "P2P Signaling Server Demo (Parallel)"
echo "=========================================="
echo ""

# Create temp files for capturing output
CLIENT1_OUT=$(mktemp)
CLIENT2_OUT=$(mktemp)

# Cleanup on exit
cleanup() {
    rm -f "$CLIENT1_OUT" "$CLIENT2_OUT"
    kill $CLIENT1_PID $CLIENT2_PID 2>/dev/null || true
}
trap cleanup EXIT

echo "Starting Client 1..."
# Keep client 1 alive for 10 seconds
(
    sleep 10
    echo "/quit"
) | ./bin/client -log-level=info 2>&1 | tee "$CLIENT1_OUT" &
CLIENT1_PID=$!

# Give client 1 time to connect and get ID
sleep 1

# Extract client 1's peer ID
PEER1_ID=$(grep "my_id" "$CLIENT1_OUT" | head -1 | sed 's/.*my_id=\([a-f0-9]*\).*/\1/')

echo "Client 1 Peer ID: ${PEER1_ID:-'(waiting...)' }"

# Wait a bit more for stable connection
sleep 1

# Re-extract if needed
if [ -z "$PEER1_ID" ]; then
    PEER1_ID=$(grep "my_id" "$CLIENT1_OUT" | head -1 | sed 's/.*my_id=\([a-f0-9]*\).*/\1/')
fi

echo "Client 1 Peer ID: $PEER1_ID"
echo ""
echo "Starting Client 2 (will auto-connect to Client 1)..."
echo ""

# Start client 2 with peer ID to connect to - stays alive for 5 seconds
(
    sleep 5
    echo "/quit"
) | ./bin/client -log-level=info -peer="$PEER1_ID" 2>&1 | tee "$CLIENT2_OUT" &
CLIENT2_PID=$!

# Let them communicate
sleep 4

# Show what happened
echo ""
echo "=========================================="
echo "Client 1 Output:"
echo "=========================================="
cat "$CLIENT1_OUT"

echo ""
echo "=========================================="
echo "Client 2 Output:"
echo "=========================================="
cat "$CLIENT2_OUT"

echo ""
echo "=========================================="
echo "Demo complete!"
echo "=========================================="
