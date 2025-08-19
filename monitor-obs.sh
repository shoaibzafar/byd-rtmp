#!/bin/bash

echo "🔍 Monitoring OBS Connection Attempts..."
echo "Server: rtmp://localhost:1935/live"
echo "Stream Key: teststream"
echo ""
echo "📊 Current server status:"
curl -s http://localhost:8080/api/public/streams | jq '.' 2>/dev/null || curl -s http://localhost:8080/api/public/streams
echo ""
echo "👀 Watching for OBS connections..."
echo "Try connecting with OBS now!"
echo ""
echo "The server will log:"
echo "  ✅ New RTMP connection from [IP]"
echo "  ✅ Handshake completed successfully"
echo "  ✅ Received RTMP command: connect"
echo "  ✅ Received RTMP command: createStream"
echo "  ✅ Received RTMP command: publish"
echo ""
echo "Press Ctrl+C to stop monitoring"

# Monitor server logs for OBS connections
while true; do
    sleep 2
    echo "--- $(date) ---"
    echo "Active streams:"
    curl -s http://localhost:8080/api/public/streams | jq '.streams | length' 2>/dev/null || echo "Error getting stream count"
    echo ""
done 