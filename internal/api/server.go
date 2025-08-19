package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"paitec-rtmp/config"
	"paitec-rtmp/internal/hls"
	"paitec-rtmp/internal/logger"

	"github.com/gorilla/websocket"
)

// Session represents a user session
type Session struct {
	ID        string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Server represents the HTTP API server
type Server struct {
	addr       string
	server     *http.Server
	rtmpServer interface {
		GetStats() map[string]interface{}
	}
	hlsServer *hls.HLSServer
	startTime time.Time
	log       *logger.Logger
	upgrader  websocket.Upgrader

	// Authentication
	sessions      map[string]*Session
	sessionsMutex sync.RWMutex
	adminUsername string
	adminPassword string
	sessionTTL    time.Duration
}

// StreamInfo represents stream information
type StreamInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created string `json:"created"`
}

// ServerStatus represents server status information
type ServerStatus struct {
	Status           string       `json:"status"`
	Uptime           string       `json:"uptime"`
	Version          string       `json:"version"`
	ActiveClients    int          `json:"activeClients"`
	ActiveStreams    int          `json:"activeStreams"`
	TotalConnections int          `json:"totalConnections"`
	Streams          []StreamInfo `json:"streams"`
}

// NewServer creates a new HTTP API server
func NewServer(addr string, rtmpServer interface {
	GetStats() map[string]interface{}
}, hlsServer *hls.HLSServer, cfg *config.Config) *Server {
	// session TTL default
	ttl := time.Duration(24) * time.Hour
	if cfg != nil && cfg.Security.WebAuth.SessionTimeout > 0 {
		ttl = time.Duration(cfg.Security.WebAuth.SessionTimeout) * time.Second
	}
	return &Server{
		addr:       addr,
		rtmpServer: rtmpServer,
		hlsServer:  hlsServer,
		startTime:  time.Now(),
		log:        logger.GetLogger(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
		sessions: make(map[string]*Session),
		adminUsername: func() string {
			if cfg != nil {
				return cfg.Security.WebAuth.Username
			}
			return "admin"
		}(),
		adminPassword: func() string {
			if cfg != nil {
				return cfg.Security.WebAuth.Password
			}
			return "admin123"
		}(),
		sessionTTL: ttl,
	}
}

// generateSessionID generates a random session ID
func (s *Server) generateSessionID() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// createSession creates a new session for a user
func (s *Server) createSession(username string) *Session {
	sessionID := s.generateSessionID()
	session := &Session{
		ID:        sessionID,
		Username:  username,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(s.sessionTTL),
	}

	s.sessionsMutex.Lock()
	s.sessions[sessionID] = session
	s.sessionsMutex.Unlock()

	return session
}

// getSession retrieves a session by ID
func (s *Server) getSession(sessionID string) *Session {
	s.sessionsMutex.RLock()
	defer s.sessionsMutex.RUnlock()

	session, exists := s.sessions[sessionID]
	if !exists {
		return nil
	}

	// Check if session has expired
	if time.Now().After(session.ExpiresAt) {
		s.sessionsMutex.RUnlock()
		s.sessionsMutex.Lock()
		delete(s.sessions, sessionID)
		s.sessionsMutex.Unlock()
		s.sessionsMutex.RLock()
		return nil
	}

	return session
}

// cleanupExpiredSessions removes expired sessions
func (s *Server) cleanupExpiredSessions() {
	s.sessionsMutex.Lock()
	defer s.sessionsMutex.Unlock()

	now := time.Now()
	for sessionID, session := range s.sessions {
		if now.After(session.ExpiresAt) {
			delete(s.sessions, sessionID)
		}
	}
}

// authMiddleware checks if the user is authenticated
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check for session cookie
		cookie, err := r.Cookie("session_id")
		if err != nil {
			// No session cookie, redirect to login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Validate session
		session := s.getSession(cookie.Value)
		if session == nil {
			// Invalid or expired session, redirect to login
			http.SetCookie(w, &http.Cookie{
				Name:    "session_id",
				Value:   "",
				Expires: time.Now().Add(-1 * time.Hour),
				Path:    "/",
			})
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Session is valid, proceed
		next.ServeHTTP(w, r)
	}
}

// Start starts the HTTP API server
func (s *Server) Start(ready chan<- struct{}) error {
	s.log.Debugf("Starting HTTP API server on %s...", s.addr)

	// Get absolute path for web directory
	webDir, err := filepath.Abs("web")
	if err != nil {
		return fmt.Errorf("failed to get web directory path: %w", err)
	}
	s.log.Debugf("Serving web files from: %s", webDir)

	mux := http.NewServeMux()

	// Public endpoints (no authentication required)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/api/login", s.handleAPILogin)
	mux.HandleFunc("/api/public/streams", s.handlePublicStreams) // Public streams endpoint

	// HLS endpoints (no authentication required)
	mux.HandleFunc("/hls/", s.handleHLS)

	// Protected endpoints (authentication required)
	mux.HandleFunc("/api/streams", s.authMiddleware(s.handleStreams))
	mux.HandleFunc("/api/v1/streams", s.authMiddleware(s.handleStreams)) // Dashboard expects this endpoint
	mux.HandleFunc("/api/status", s.authMiddleware(s.handleStatus))
	mux.HandleFunc("/api/v1/stats", s.authMiddleware(s.handleStats))          // Dashboard expects this endpoint
	mux.HandleFunc("/api/v1/stats/json", s.authMiddleware(s.handleStatsJSON)) // JSON-only endpoint
	mux.HandleFunc("/api/v1/ws", s.authMiddleware(s.handleWebSocket))         // WebSocket endpoint

	// Protected pages (authentication required)
	mux.HandleFunc("/dashboard.html", s.authMiddleware(s.handleDashboard))
	mux.HandleFunc("/stats", s.authMiddleware(s.handleStatsPage))

	// Serve static files (dashboard) - use absolute path
	fileServer := http.FileServer(http.Dir(webDir))

	// Handle all other routes with the file server
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// If the request is for an API endpoint, don't serve files
		if r.URL.Path == "/health" || r.URL.Path == "/login" || r.URL.Path == "/logout" ||
			r.URL.Path == "/api/login" || r.URL.Path == "/api/streams" || r.URL.Path == "/api/status" ||
			r.URL.Path == "/api/v1/stats" || r.URL.Path == "/api/v1/stats/json" || r.URL.Path == "/api/v1/ws" || r.URL.Path == "/api/v1/streams" ||
			r.URL.Path == "/dashboard.html" || r.URL.Path == "/stats" || strings.HasPrefix(r.URL.Path, "/hls/") {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	s.log.Infof("HTTP API server listening on %s", s.addr)

	// Start session cleanup goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Hour) // Clean up every hour
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.cleanupExpiredSessions()
			}
		}
	}()

	// Signal that the server is ready
	if ready != nil {
		ready <- struct{}{}
	}

	return s.server.ListenAndServe()
}

// Stop stops the HTTP API server
func (s *Server) Stop() error {
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// handleLogin handles the login page
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Check if user is already logged in
		cookie, err := r.Cookie("session_id")
		if err == nil {
			session := s.getSession(cookie.Value)
			if session != nil {
				// Already logged in, redirect to dashboard
				http.Redirect(w, r, "/dashboard.html", http.StatusSeeOther)
				return
			}
		}

		// Serve login page
		http.ServeFile(w, r, "web/login.html")
		return
	}

	// POST request should be handled by /api/login
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleLogout handles logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Remove session cookie
	http.SetCookie(w, &http.Cookie{
		Name:    "session_id",
		Value:   "",
		Expires: time.Now().Add(-1 * time.Hour),
		Path:    "/",
	})

	// Redirect to login page
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleAPILogin handles login API requests
func (s *Server) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	// Simple authentication (in production, use proper password hashing)
	if username == s.adminUsername && password == s.adminPassword {
		// Create session
		session := s.createSession(username)

		// Set session cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    session.ID,
			Expires:  session.ExpiresAt,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
		})

		// Return success response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Login successful",
		})
		return
	}

	// Authentication failed
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"message": "Invalid username or password",
	})
}

// handleDashboard serves the dashboard page (protected)
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/dashboard.html")
}

// handleStatsPage serves the stats page (protected)
func (s *Server) handleStatsPage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/stats.html")
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	response := map[string]interface{}{
		"status":  "ok",
		"message": "Server is running",
	}

	json.NewEncoder(w).Encode(response)
}

// handleStreams handles stream information requests
func (s *Server) handlePublicStreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Get streams from RTMP server
	rtmpStats := s.rtmpServer.GetStats()
	rawStreams, ok := rtmpStats["streams"].([]map[string]interface{})
	if !ok {
		rawStreams = []map[string]interface{}{}
	}

	response := map[string]interface{}{
		"streams": rawStreams,
		"status":  "running",
		"message": "RTMP streams are working!",
	}

	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleStreams(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Get streams from RTMP server
	rtmpStats := s.rtmpServer.GetStats()
	rawStreams, ok := rtmpStats["streams"].([]map[string]interface{})
	if !ok {
		rawStreams = []map[string]interface{}{}
	}

	response := map[string]interface{}{
		"streams": rawStreams,
		"status":  "running",
	}

	json.NewEncoder(w).Encode(response)
}

// handleStatus handles server status requests
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	uptime := time.Since(s.startTime)
	uptimeStr := formatDuration(uptime)

	rtmpStats := s.rtmpServer.GetStats()
	status := ServerStatus{
		Status:           "Online",
		Uptime:           uptimeStr,
		Version:          "1.0.0",
		ActiveClients:    toInt(rtmpStats["activeClients"]),
		ActiveStreams:    toInt(rtmpStats["activeStreams"]),
		TotalConnections: toInt(rtmpStats["totalConnections"]),
	}

	json.NewEncoder(w).Encode(status)
}

// handleStats handles the stats endpoint that the dashboard expects
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	// Check if this is a browser request (wants HTML) or API request (wants JSON)
	acceptHeader := r.Header.Get("Accept")
	userAgent := r.Header.Get("User-Agent")

	// If it's a browser request, serve the beautiful stats page
	if strings.Contains(acceptHeader, "text/html") ||
		strings.Contains(userAgent, "Mozilla") ||
		strings.Contains(userAgent, "Chrome") ||
		strings.Contains(userAgent, "Safari") ||
		strings.Contains(userAgent, "Firefox") {

		// Serve the beautiful stats page
		http.ServeFile(w, r, "web/stats.html")
		return
	}

	// Otherwise, serve JSON for API requests
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	uptime := time.Since(s.startTime)
	uptimeStr := formatDuration(uptime)

	// Get stats from RTMP server
	rtmpStats := s.rtmpServer.GetStats()

	stats := map[string]interface{}{
		"status":           "Online",
		"uptime":           uptimeStr,
		"version":          "1.0.0",
		"activeClients":    toInt(rtmpStats["activeClients"]),
		"activeStreams":    toInt(rtmpStats["activeStreams"]),
		"totalConnections": toInt(rtmpStats["totalConnections"]),
		"streams":          rtmpStats["streams"],
	}

	json.NewEncoder(w).Encode(stats)
}

// handleStatsJSON handles the stats endpoint that the dashboard expects, but only for JSON requests
func (s *Server) handleStatsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	uptime := time.Since(s.startTime)
	uptimeStr := formatDuration(uptime)

	// Get stats from RTMP server
	rtmpStats := s.rtmpServer.GetStats()

	stats := map[string]interface{}{
		"status":           "Online",
		"uptime":           uptimeStr,
		"version":          "1.0.0",
		"activeClients":    toInt(rtmpStats["activeClients"]),
		"activeStreams":    toInt(rtmpStats["activeStreams"]),
		"totalConnections": toInt(rtmpStats["totalConnections"]),
		"streams":          rtmpStats["streams"],
	}

	json.NewEncoder(w).Encode(stats)
}

// handleWebSocket handles WebSocket connections
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Errorf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	s.log.Debugf("WebSocket connection established")

	// Send initial status
	uptime := time.Since(s.startTime)
	uptimeStr := formatDuration(uptime)

	// initial status
	stats := s.rtmpServer.GetStats()
	status := map[string]interface{}{
		"type": "stats",
		"data": map[string]interface{}{
			"status":           "Online",
			"uptime":           uptimeStr,
			"version":          "1.0.0",
			"activeClients":    toInt(stats["activeClients"]),
			"activeStreams":    toInt(stats["activeStreams"]),
			"totalConnections": toInt(stats["totalConnections"]),
		},
	}

	if err := conn.WriteJSON(status); err != nil {
		s.log.Errorf("Failed to send WebSocket message: %v", err)
		return
	}

	// Keep connection alive and send periodic updates
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			uptime := time.Since(s.startTime)
			uptimeStr := formatDuration(uptime)

			sstats := s.rtmpServer.GetStats()
			update := map[string]interface{}{
				"type": "stats",
				"data": map[string]interface{}{
					"status":           "Online",
					"uptime":           uptimeStr,
					"version":          "1.0.0",
					"activeClients":    toInt(sstats["activeClients"]),
					"activeStreams":    toInt(sstats["activeStreams"]),
					"totalConnections": toInt(sstats["totalConnections"]),
				},
			}

			if err := conn.WriteJSON(update); err != nil {
				s.log.Errorf("Failed to send WebSocket update: %v", err)
				return
			}
		}
	}
}

// formatDuration formats duration in a human-readable format
func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
	} else if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	} else {
		return fmt.Sprintf("%ds", seconds)
	}
}

// toInt safely converts interface{} to int
func toInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float32:
		return int(t)
	case float64:
		return int(t)
	case uint:
		return int(t)
	case uint32:
		return int(t)
	case uint64:
		return int(t)
	default:
		return 0
	}
}

// handleHLS handles HLS requests for playlists and segments
func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	if s.hlsServer == nil {
		http.Error(w, "HLS server not available", http.StatusServiceUnavailable)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/hls/")
	if path == "" {
		http.Error(w, "Invalid HLS path", http.StatusBadRequest)
		return
	}

	// Split path to get stream name and file
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid HLS path", http.StatusBadRequest)
		return
	}

	streamName := parts[0]
	fileName := parts[1]

	// Handle playlist request (.m3u8)
	if strings.HasSuffix(fileName, ".m3u8") {
		// First try to get from active HLS server
		playlist, err := s.hlsServer.GeneratePlaylist(streamName)
		if err != nil {
			// If not found, try to serve static file
			hlsDir := "./hls"
			filePath := filepath.Join(hlsDir, streamName, fileName)
			if _, err := os.Stat(filePath); err == nil {
				http.ServeFile(w, r, filePath)
				return
			}
			http.Error(w, "Stream not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte(playlist))
		return
	}

	// Handle segment request (.ts)
	if strings.HasSuffix(fileName, ".ts") {
		// First try to get from active HLS server
		segmentPath := s.hlsServer.GetSegmentPath(streamName, fileName)
		if _, err := os.Stat(segmentPath); err == nil {
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			http.ServeFile(w, r, segmentPath)
			return
		}

		// If not found, try to serve static file
		hlsDir := "./hls"
		filePath := filepath.Join(hlsDir, streamName, fileName)
		if _, err := os.Stat(filePath); err == nil {
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			http.ServeFile(w, r, filePath)
			return
		}

		http.Error(w, "Segment not found", http.StatusNotFound)
		return
	}

	http.Error(w, "Invalid file type", http.StatusBadRequest)
}
