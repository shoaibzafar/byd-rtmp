#!/bin/bash

echo "🔍 OBS Connection Test - Comprehensive"
echo "====================================="
echo ""
echo "📊 Server Status:"
curl -s http://localhost:8080/health
echo ""
echo ""
echo "📺 OBS Settings (VERIFY THESE EXACTLY):"
echo "  Service: Custom"
echo "  Server: rtmp://localhost:1935/live"
echo "  Stream Key: teststream"
echo "  Authentication: DISABLED"
echo ""
echo "🔧 Testing connectivity..."
echo ""

# Test basic connectivity
echo "1. Testing RTMP port..."
nc -z localhost 1935 && echo "✅ Port 1935 is open" || echo "❌ Port 1935 is closed"

echo ""
echo "2. Testing HTTP API..."
curl -s http://localhost:8080/api/public/streams | jq '.' 2>/dev/null || echo "❌ HTTP API not responding"

echo ""
echo "3. Current streams:"
curl -s http://localhost:8080/api/public/streams | jq '.streams | length' 2>/dev/null || echo "0"

echo ""
echo "🎯 Now try connecting with OBS..."
echo "The server will show detailed logs about what OBS is sending."
echo ""
echo "If OBS fails, we'll see exactly what's happening in the logs."
echo ""
echo "Press Ctrl+C to stop monitoring"

# Monitor for connections
while true; do
    sleep 3
    echo "--- $(date) ---"
    echo "Active streams: $(curl -s http://localhost:8080/api/public/streams | jq '.streams | length' 2>/dev/null || echo "0")"
done 