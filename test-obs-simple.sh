#!/bin/bash

echo "=== OBS Connection Test ==="
echo "Server is running and ready for OBS connection"
echo ""
echo "OBS Settings:"
echo "  Service: Custom"
echo "  Server: rtmp://localhost:1935/live"
echo "  Stream Key: teststream"
echo ""
echo "Complete URL: rtmp://localhost:1935/live/teststream"
echo ""
echo "Current server status:"
curl -s http://localhost:8080/api/public/streams | jq '.' 2>/dev/null || curl -s http://localhost:8080/api/public/streams
echo ""
echo "Now try connecting with OBS..."
echo "The server will log all connection attempts and commands."
echo ""
echo "If OBS still fails, we'll see the exact error in the logs." 