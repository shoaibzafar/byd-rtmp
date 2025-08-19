#!/bin/bash

echo "🎥 RTMP Stream Testing Script"
echo "============================="
echo ""

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# Function to check if server is running
check_server() {
    echo -e "${BLUE}🔍 Checking server status...${NC}"
    if curl -s http://localhost:8080/health > /dev/null; then
        echo -e "${GREEN}✅ Server is running${NC}"
        return 0
    else
        echo -e "${RED}❌ Server is not running${NC}"
        echo "Start the server with: docker-compose up -d"
        return 1
    fi
}

# Function to show current stats
show_stats() {
    echo -e "${BLUE}📊 Current Server Statistics:${NC}"
    curl -s http://localhost:8080/api/v1/stats | jq .
    echo ""
}

# Function to show current streams
show_streams() {
    echo -e "${BLUE}📺 Current Active Streams:${NC}"
    curl -s http://localhost:8080/api/v1/streams | jq .
    echo ""
}

# Function to test with FFmpeg
test_ffmpeg() {
    local stream_name=$1
    local duration=${2:-30}
    
    echo -e "${YELLOW}🎬 Testing with FFmpeg: $stream_name (${duration}s)${NC}"
    
    if ! command -v ffmpeg &> /dev/null; then
        echo -e "${RED}❌ FFmpeg not found. Install it first:${NC}"
        echo "   macOS: brew install ffmpeg"
        echo "   Ubuntu: sudo apt install ffmpeg"
        echo "   Windows: Download from https://ffmpeg.org/download.html"
        return 1
    fi
    
    echo "Starting test stream..."
    echo "RTMP URL: rtmp://localhost:1935/live/$stream_name"
    echo "Expected structured name: school_001/$stream_name"
    echo ""
    
    # Start FFmpeg test stream
    ffmpeg -f lavfi -i testsrc=duration=$duration:size=640x480:rate=15 \
           -c:v libx264 -preset ultrafast -tune zerolatency \
           -f flv rtmp://localhost:1935/live/$stream_name
    
    echo ""
    echo -e "${GREEN}✅ Test stream completed${NC}"
}

# Function to show testing options
show_options() {
    echo -e "${YELLOW}🎯 Testing Options:${NC}"
    echo ""
    echo "1. Quick Test (30s):"
    echo "   ./test-stream.sh quick"
    echo ""
    echo "2. Extended Test (2 minutes):"
    echo "   ./test-stream.sh extended"
    echo ""
    echo "3. Multiple Streams Test:"
    echo "   ./test-stream.sh multiple"
    echo ""
    echo "4. Custom Stream Test:"
    echo "   ./test-stream.sh custom <stream_name> <duration>"
    echo ""
    echo "5. Monitor Only:"
    echo "   ./test-stream.sh monitor"
    echo ""
}

# Function to monitor streams
monitor_streams() {
    echo -e "${YELLOW}📊 Monitoring Streams (Press Ctrl+C to stop)${NC}"
    echo ""
    
    while true; do
        clear
        echo "🎥 RTMP Stream Monitor"
        echo "======================"
        echo ""
        
        # Show stats
        echo -e "${BLUE}Server Stats:${NC}"
        curl -s http://localhost:8080/api/v1/stats | jq .
        echo ""
        
        # Show streams
        echo -e "${BLUE}Active Streams:${NC}"
        curl -s http://localhost:8080/api/v1/streams | jq .
        echo ""
        
        echo "Refreshing in 5 seconds... (Ctrl+C to stop)"
        sleep 5
    done
}

# Function to test multiple streams
test_multiple() {
    echo -e "${YELLOW}🎬 Testing Multiple Streams${NC}"
    echo ""
    
    # Start Camera 1
    echo "Starting Camera 1: camipcet1"
    ffmpeg -f lavfi -i testsrc=duration=120:size=640x480:rate=15 \
           -c:v libx264 -preset ultrafast -tune zerolatency \
           -f flv rtmp://localhost:1935/live/camipcet1 &
    CAM1_PID=$!
    
    sleep 2
    
    # Start Camera 2
    echo "Starting Camera 2: camipcet2"
    ffmpeg -f lavfi -i testsrc=duration=120:size=640x480:rate=15 \
           -c:v libx264 -preset ultrafast -tune zerolatency \
           -f flv rtmp://localhost:1935/live/camipcet2 &
    CAM2_PID=$!
    
    sleep 2
    
    # Start Camera 3
    echo "Starting Camera 3: camera001"
    ffmpeg -f lavfi -i testsrc=duration=120:size=640x480:rate=15 \
           -c:v libx264 -preset ultrafast -tune zerolatency \
           -f flv rtmp://localhost:1935/live/camera001 &
    CAM3_PID=$!
    
    echo ""
    echo -e "${GREEN}✅ All streams started!${NC}"
    echo "Check dashboard at: http://localhost"
    echo "Login: admin / paitec123"
    echo ""
    echo "Streams should appear as:"
    echo "  - school_001/camipcet1 (Original: camipcet1)"
    echo "  - school_001/camipcet2 (Original: camipcet2)"
    echo "  - school_001/camera_001 (Original: camera001)"
    echo ""
    echo "Press Ctrl+C to stop all streams"
    
    # Wait for user to stop
    wait
}

# Main execution
main() {
    case "$1" in
        "quick")
            if check_server; then
                show_stats
                test_ffmpeg "camipcet1" 30
                show_streams
            fi
            ;;
        "extended")
            if check_server; then
                show_stats
                test_ffmpeg "camipcet1" 120
                show_streams
            fi
            ;;
        "multiple")
            if check_server; then
                show_stats
                test_multiple
            fi
            ;;
        "custom")
            if [ -z "$2" ]; then
                echo -e "${RED}❌ Please provide stream name${NC}"
                echo "Usage: ./test-stream.sh custom <stream_name> [duration]"
                exit 1
            fi
            if check_server; then
                show_stats
                test_ffmpeg "$2" "${3:-60}"
                show_streams
            fi
            ;;
        "monitor")
            if check_server; then
                monitor_streams
            fi
            ;;
        *)
            if check_server; then
                show_stats
                show_streams
                show_options
            fi
            ;;
    esac
}

# Run main function
main "$@"