#!/bin/bash
# Demo script showing two clients discovering each other

echo "=========================================="
echo "P2P Signaling Server Demo"
echo "=========================================="
echo ""
echo "This demo will:"
echo "1. Start Client 1 and let it connect to the server"
echo "2. Start Client 2 - it should see Client 1 as a peer"
echo "3. Client 2 will send an offer to Client 1"
echo "4. Client 1 auto-responds with an answer"
echo ""
echo "=========================================="
echo ""

# Start client 1 in background
(
    echo ""
    sleep 5
    echo "/quit"
) | ./bin/client -log-level=info 

echo ""
echo "=========================================="
echo "Client 1 finished. Now starting Client 2..."
echo "=========================================="
echo ""

# Start client 2 in background  
(
    echo ""
    sleep 3  
    echo "/quit"
) | ./bin/client -log-level=info

echo ""
echo "=========================================="
echo "Demo complete!"
echo "=========================================="
