#!/bin/bash

echo "🔍 OBS Connection Debug Test"
echo "============================"
echo ""
echo "📊 Server Status:"
curl -s http://localhost:8080/health
echo ""
echo ""
echo "📺 OBS Settings to use:"
echo "  Service: Custom"
echo "  Server: rtmp://localhost:1935/live"
echo "  Stream Key: teststream"
echo "  Authentication: DISABLED"
echo ""
echo "🔧 Testing server connectivity..."
echo ""

# Test basic connectivity
echo "1. Testing basic RTMP port connectivity..."
nc -z localhost 1935 && echo "✅ Port 1935 is open" || echo "❌ Port 1935 is closed"

echo ""
echo "2. Testing HTTP API..."
curl -s http://localhost:8080/api/public/streams | jq '.' 2>/dev/null || echo "❌ HTTP API not responding"

echo ""
echo "3. Current active streams:"
curl -s http://localhost:8080/api/public/streams | jq '.streams | length' 2>/dev/null || echo "0"

echo ""
echo "🎯 Now try connecting with OBS..."
echo "The server will log all connection attempts."
echo ""
echo "If OBS fails, we'll see the exact error in the logs."
echo ""
echo "Press Ctrl+C to stop monitoring"

# Monitor for connections
while true; do
    sleep 2
    echo "--- $(date) ---"
    echo "Active streams: $(curl -s http://localhost:8080/api/public/streams | jq '.streams | length' 2>/dev/null || echo "0")"
done 