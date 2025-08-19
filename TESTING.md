# 🧪 RTMP Server Testing Guide

## ✅ **Quick Status Check**

```bash
# Check if containers are running
docker-compose ps

# Test health endpoint
curl -s http://localhost:8080/health | jq .

# Check server stats
curl -s http://localhost:8080/api/v1/stats | jq .
```

## 🌐 **Web Dashboard Testing**

1. **Open your browser** and navigate to: `http://localhost/`
2. **Expected features:**
   - Server status indicator (Online/Offline)
   - Real-time statistics (Clients, Streams, Uptime)
   - Control buttons (Refresh Stats, Test Connection, Stream Info)
   - Active streams list (empty initially)

3. **Test buttons:**
   - Click "🔄 Refresh Stats" - should update statistics
   - Click "🔗 Test Connection" - should show success message
   - Click "📺 Stream Info" - should display streaming instructions

## 📺 **Streaming Tests**

### **Option 1: FFmpeg (Recommended)**

1. **Install FFmpeg:**
   ```bash
   # macOS
   brew install ffmpeg
   
   # Ubuntu/Debian
   sudo apt install ffmpeg
   
   # Windows
   # Download from https://ffmpeg.org/download.html
   ```

2. **Create test stream:**
   ```bash
   # Generate a test pattern video with audio
   ffmpeg -f lavfi -i testsrc=duration=60:size=640x480:rate=30 \
          -f lavfi -i sine=frequency=1000:duration=60 \
          -c:v libx264 -c:a aac -f flv \
          rtmp://localhost:1935/live/test-stream
   ```

3. **Stream existing video file:**
   ```bash
   ffmpeg -re -i your-video.mp4 -c copy -f flv \
          rtmp://localhost:1935/live/my-stream
   ```

### **Option 2: OBS Studio**

1. **Download OBS Studio:** https://obsproject.com/
2. **Configure streaming:**
   - Go to `Settings` → `Stream`
   - Service: `Custom`
   - Server: `rtmp://localhost:1935/live`
   - Stream Key: `your-stream-name`
3. **Start streaming** and monitor the dashboard

### **Option 3: VLC (for testing playback)**

1. **Open VLC Media Player**
2. **Go to:** `Media` → `Open Network Stream`
3. **Enter URL:** `rtmp://localhost:1935/live/your-stream-name`
4. **Click Play**

## 🔍 **Verification Steps**

### **1. Monitor Active Streams**
```bash
# Check if streams are detected
curl -s http://localhost:8080/api/v1/streams | jq .

# Expected output when streaming:
{
  "count": 1,
  "streams": [
    {
      "id": 1,
      "name": "test-stream",
      "created": "2025-08-03T07:00:00Z"
    }
  ]
}
```

### **2. Monitor Connected Clients**
```bash
# Check active clients
curl -s http://localhost:8080/api/v1/clients | jq .

# Expected output when clients connected:
{
  "count": 1,
  "clients": [
    {
      "id": "client-123",
      "remoteAddr": "127.0.0.1:12345",
      "connected": "2025-08-03T07:00:00Z"
    }
  ]
}
```

### **3. Real-time Statistics**
```bash
# Monitor server stats during streaming
curl -s http://localhost:8080/api/v1/stats | jq .

# Should show:
{
  "stats": {
    "clients": 1,    # Number of connected clients
    "streams": 1,    # Number of active streams
    "uptime": "5m30s"
  }
}
```

### **4. Web Dashboard Verification**

During streaming, the web dashboard should show:
- ✅ Server Status: **Online**
- 📊 Active Clients: **1** (or more)
- 📺 Active Streams: **1** (or more)
- 📋 Stream list with your stream name and creation time

## 🚨 **Troubleshooting**

### **Connection Issues**
```bash
# Test if RTMP port is open
nc -z localhost 1935
# Should output: Connection succeeded

# Check container logs
docker-compose logs rtmp-server

# Check if containers are running
docker-compose ps
```

### **Streaming Issues**
- **FFmpeg fails:** Check if the RTMP URL is correct
- **OBS can't connect:** Verify server and stream key settings
- **VLC can't play:** Ensure the stream name matches exactly

### **API Issues**
```bash
# Test all endpoints
curl -s http://localhost:8080/health
curl -s http://localhost:8080/api/v1/stats
curl -s http://localhost:8080/api/v1/streams
curl -s http://localhost:8080/api/v1/clients
```

## 📱 **Mobile Testing**

If you want to test from mobile devices on the same network:

1. **Find your computer's IP address:**
   ```bash
   # macOS/Linux
   ifconfig | grep "inet " | grep -v 127.0.0.1
   
   # Windows
   ipconfig
   ```

2. **Use your IP instead of localhost:**
   - RTMP URL: `rtmp://YOUR_IP:1935/live/stream-name`
   - Web Dashboard: `http://YOUR_IP/`
   - API: `http://YOUR_IP:8080/api/v1/stats`

## 🎯 **Success Criteria**

Your RTMP server is working correctly if:

- ✅ All containers are running and healthy
- ✅ Web dashboard loads and displays server information
- ✅ API endpoints respond with valid JSON
- ✅ RTMP port 1935 accepts connections
- ✅ Streaming software can connect and publish
- ✅ Statistics update in real-time during streaming
- ✅ Multiple streams can be handled simultaneously

## 🔄 **Continuous Testing**

For ongoing monitoring:

```bash
# Watch logs in real-time
docker-compose logs -f rtmp-server

# Monitor stats every 5 seconds
watch -n 5 'curl -s http://localhost:8080/api/v1/stats | jq .'

# Check stream count periodically
watch -n 2 'curl -s http://localhost:8080/api/v1/streams | jq .count'
```

---

**🎉 Congratulations!** Your RTMP server is now fully tested and ready for production use!