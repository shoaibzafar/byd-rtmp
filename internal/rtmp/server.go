package rtmp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"time"

	"paitec-rtmp/internal/hls"
	"paitec-rtmp/internal/logger"
)

// Server represents the RTMP server
type Server struct {
	addr     string
	listener net.Listener
	streams  map[string]*Stream
	mu       sync.RWMutex
	log      *logger.Logger
	hls      *hls.HLSServer
}

// Stream represents an RTMP stream
type Stream struct {
	ID        string
	Publisher *RTMPConnection
	Players   []*RTMPConnection
	mu        sync.RWMutex
}

// NewServer creates a new RTMP server
func NewServer(addr string, hlsServer *hls.HLSServer) *Server {
	return &Server{
		addr:    addr,
		streams: make(map[string]*Stream),
		log:     logger.GetLogger(),
		hls:     hlsServer,
	}
}

// Start starts the RTMP server
func (s *Server) Start(ready chan<- struct{}) error {
	var err error
	s.listener, err = net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to start RTMP server: %w", err)
	}

	s.log.Infof("RTMP server listening on %s", s.addr)

	// Signal that the server is ready
	if ready != nil {
		ready <- struct{}{}
	}

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.log.Errorf("Failed to accept connection: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

// Stop stops the RTMP server
func (s *Server) Stop() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// GetStats returns server statistics
func (s *Server) GetStats() map[string]interface{} {
	s.mu.RLock()

	activeStreams := 0
	activeClients := 0
	streams := []map[string]interface{}{}

	s.log.Debugf("GetStats: Found %d streams in server", len(s.streams))

	for streamID, stream := range s.streams {
		activeStreams++
		if stream.Publisher != nil {
			activeClients++
		}
		stream.mu.RLock()
		activeClients += len(stream.Players)
		playersCount := len(stream.Players)
		stream.mu.RUnlock()

		streamInfo := map[string]interface{}{
			"id":      stream.ID,
			"name":    stream.ID,
			"created": time.Now().Format(time.RFC3339),
			"players": playersCount,
		}
		streams = append(streams, streamInfo)
		s.log.Debugf("GetStats: Stream %s has %d players", streamID, playersCount)
	}

	s.log.Debugf("GetStats: Returning %d active streams, %d active clients", activeStreams, activeClients)

	result := map[string]interface{}{
		"activeStreams":    activeStreams,
		"activeClients":    activeClients,
		"totalConnections": activeClients,
		"streams":          streams,
	}
	s.mu.RUnlock()
	return result
}

// handleConnection handles a new RTMP connection
func (s *Server) handleConnection(conn net.Conn) {
	// Set connection timeouts to prevent hanging connections
	conn.SetReadDeadline(time.Now().Add(5 * time.Minute))   // 5 minutes read timeout
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second)) // 30 seconds write timeout

	rtmpConn := NewRTMPConnection(conn, s.log)
	defer rtmpConn.Close()

	s.log.Infof("New RTMP connection from %s", conn.RemoteAddr())

	// Handshake
	s.log.Debugf("Starting handshake for connection from %s", conn.RemoteAddr())
	if err := s.doHandshake(rtmpConn); err != nil {
		s.log.Errorf("Handshake failed for %s: %v", conn.RemoteAddr(), err)
		return
	}

	s.log.Infof("Handshake completed successfully for %s", conn.RemoteAddr())

	// Main message loop
	s.log.Debugf("Starting message loop for %s", conn.RemoteAddr())

	// Cleanup streams when connection closes
	defer func() {
		s.log.Debugf("Cleaning up streams for connection %s", conn.RemoteAddr())
		s.mu.Lock()
		for streamID, stream := range s.streams {
			if stream.Publisher != nil && stream.Publisher.conn == conn {
				s.log.Infof("Removing disconnected stream: %s", streamID)
				delete(s.streams, streamID)
				break
			}
		}
		s.mu.Unlock()
	}()

	for {
		// Reset read deadline for each message to keep connection alive
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))

		msg, err := rtmpConn.ReadMessage()
		if err != nil {
			if err == io.EOF {
				s.log.Infof("Connection closed by client %s", conn.RemoteAddr())
				return
			}
			s.log.Errorf("Failed to read message from %s: %v", conn.RemoteAddr(), err)
			return
		}

		s.log.Debugf("Received message from %s: Type=%d, Length=%d",
			conn.RemoteAddr(), msg.Header.MessageTypeID, msg.Header.MessageLength)

		// Reset write deadline before handling message
		conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

		if err := s.handleMessage(rtmpConn, msg); err != nil {
			s.log.Errorf("Failed to handle message from %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
}

// doHandshake performs the RTMP handshake
func (s *Server) doHandshake(conn *RTMPConnection) error {
	s.log.Debugf("Starting RTMP handshake...")

	// C0: version - be more robust with different clients
	version := make([]byte, 1)
	if _, err := conn.conn.Read(version); err != nil {
		// Some clients might send different data or close immediately
		s.log.Debugf("Failed to read C0: %v - client might be using different protocol", err)
		return fmt.Errorf("failed to read C0: %w", err)
	}
	s.log.Debugf("Received C0: version=%d (0x%x)", version[0], version[0])

	// S0: version - be more lenient with version compatibility
	clientVersion := version[0]
	if clientVersion != RTMP_VERSION {
		s.log.Debugf("Client version %d (0x%x) differs from server version %d (0x%x), but continuing",
			clientVersion, clientVersion, RTMP_VERSION, RTMP_VERSION)
	}

	// Send S0 with the same version the client sent (for better compatibility)
	responseVersion := clientVersion
	if _, err := conn.conn.Write([]byte{responseVersion}); err != nil {
		return fmt.Errorf("failed to write S0: %w", err)
	}
	s.log.Debugf("Sent S0: version=%d (0x%x)", responseVersion, responseVersion)

	// C1: time + zero + random
	c1 := make([]byte, 1536)
	if _, err := io.ReadFull(conn.conn, c1); err != nil {
		return fmt.Errorf("failed to read C1: %w", err)
	}
	s.log.Debugf("Received C1 (%d bytes)", len(c1))

	// S1: time + zero + random
	s1 := make([]byte, 1536)
	// Fill with random data
	if _, err := rand.Read(s1); err != nil {
		return fmt.Errorf("failed to generate random data for S1: %w", err)
	}
	// Set time (first 4 bytes)
	binary.BigEndian.PutUint32(s1[:4], uint32(time.Now().Unix()))
	if _, err := conn.conn.Write(s1); err != nil {
		return fmt.Errorf("failed to write S1: %w", err)
	}
	s.log.Debugf("Sent S1 (%d bytes)", len(s1))

	// C2: should contain our S1 (but some clients don't send it properly)
	// Set a timeout for reading C2 and ignore if not present
	conn.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	c2 := make([]byte, 1536)
	n, err := conn.conn.Read(c2)
	if err != nil {
		// If we can't read C2, just continue - some clients don't send it
		s.log.Debugf("Could not read C2 (this is normal for some clients): %v", err)
		// Send S2 anyway to complete the handshake
		if _, err := conn.conn.Write(c1); err != nil {
			return fmt.Errorf("failed to write S2: %w", err)
		}
		s.log.Debugf("Sent S2 (%d bytes) after C2 timeout", len(c1))
	} else {
		s.log.Debugf("Received C2 (%d bytes)", n)
		// S2: echo C1 (send S2 after receiving C2 per RTMP spec)
		if _, err := conn.conn.Write(c1); err != nil {
			return fmt.Errorf("failed to write S2: %w", err)
		}
		s.log.Debugf("Sent S2 (%d bytes) after C2", len(c1))
	}

	// Clear the read deadline
	conn.conn.SetReadDeadline(time.Time{})

	// Don't send protocol control messages immediately - wait for client to initiate
	// This prevents the client from disconnecting during handshake

	s.log.Debugf("RTMP handshake completed successfully")
	return nil
}

// sendProtocolControlMessages sends required protocol control messages after handshake
func (s *Server) sendProtocolControlMessages(conn *RTMPConnection) error {
	// Send Set Chunk Size
	chunkSizeMsg := &RTMPMessage{
		Header: MessageHeader{
			MessageTypeID:   MSG_SET_CHUNK_SIZE,
			ChunkStreamID:   CSID_PROTOCOL_CONTROL,
			MessageLength:   4,
			MessageStreamID: 0,
		},
		Payload: make([]byte, 4),
	}
	binary.BigEndian.PutUint32(chunkSizeMsg.Payload, 4096) // Set chunk size to 4KB
	if err := conn.WriteMessage(chunkSizeMsg); err != nil {
		return fmt.Errorf("failed to send Set Chunk Size: %w", err)
	}
	s.log.Debugf("Sent Set Chunk Size: 4096")

	// Send Window Acknowledgement Size
	windowAckMsg := &RTMPMessage{
		Header: MessageHeader{
			MessageTypeID:   MSG_WINDOW_ACK_SIZE,
			ChunkStreamID:   CSID_PROTOCOL_CONTROL,
			MessageLength:   4,
			MessageStreamID: 0,
		},
		Payload: make([]byte, 4),
	}
	binary.BigEndian.PutUint32(windowAckMsg.Payload, 2500000) // 2.5MB window
	if err := conn.WriteMessage(windowAckMsg); err != nil {
		return fmt.Errorf("failed to send Window Acknowledgement Size: %w", err)
	}
	s.log.Debugf("Sent Window Acknowledgement Size: 2500000")

	// Send Set Peer Bandwidth
	peerBandwidthMsg := &RTMPMessage{
		Header: MessageHeader{
			MessageTypeID:   MSG_SET_PEER_BANDWIDTH,
			ChunkStreamID:   CSID_PROTOCOL_CONTROL,
			MessageLength:   5,
			MessageStreamID: 0,
		},
		Payload: make([]byte, 5),
	}
	binary.BigEndian.PutUint32(peerBandwidthMsg.Payload[:4], 2500000) // 2.5MB bandwidth
	peerBandwidthMsg.Payload[4] = 2                                   // Dynamic limit type
	if err := conn.WriteMessage(peerBandwidthMsg); err != nil {
		return fmt.Errorf("failed to send Set Peer Bandwidth: %w", err)
	}
	s.log.Debugf("Sent Set Peer Bandwidth: 2500000, type: 2")

	return nil
}

// handleMessage handles an RTMP message
func (s *Server) handleMessage(conn *RTMPConnection, msg *RTMPMessage) error {
	switch msg.Header.MessageTypeID {
	case MSG_SET_CHUNK_SIZE:
		// Handle Set Chunk Size message
		if len(msg.Payload) >= 4 {
			chunkSize := binary.BigEndian.Uint32(msg.Payload)
			conn.chunkSize = chunkSize
			s.log.Debugf("Set chunk size to: %d", chunkSize)
		}
		return nil
	case MSG_WINDOW_ACK_SIZE:
		// Handle Window Acknowledgement Size message
		if len(msg.Payload) >= 4 {
			windowSize := binary.BigEndian.Uint32(msg.Payload)
			conn.windowAckSize = windowSize
			s.log.Debugf("Set window acknowledgement size to: %d", windowSize)
		}
		return nil
	case MSG_ACK:
		// Handle Acknowledgement message - just log it
		if len(msg.Payload) >= 4 {
			seqNum := binary.BigEndian.Uint32(msg.Payload)
			s.log.Debugf("Received acknowledgement for sequence: %d", seqNum)
		}
		return nil
	case MSG_AMF0_COMMAND_MESSAGE:
		s.log.Debugf("Received AMF0 command message, length: %d bytes", len(msg.Payload))
		return s.handleCommandMessage(conn, msg)
	case MSG_VIDEO_MESSAGE:
		return s.handleVideoMessage(conn, msg)
	case MSG_AUDIO_MESSAGE:
		return s.handleAudioMessage(conn, msg)
	case MSG_VIDEO_MESSAGE_RTMP:
		return s.handleVideoMessage(conn, msg)
	case MSG_AUDIO_MESSAGE_RTMP:
		return s.handleAudioMessage(conn, msg)
	case MSG_VIDEO_MESSAGE_RTMP2:
		return s.handleVideoMessage(conn, msg)
	case MSG_VIDEO_MESSAGE_RTMP3:
		return s.handleVideoMessage(conn, msg)
	case MSG_AMF0_DATA_MESSAGE:
		s.log.Debugf("Received AMF0 data message, length: %d bytes", len(msg.Payload))
		// Handle AMF0 data messages (metadata, etc.)
		return nil
	case MSG_AMF3_DATA_MESSAGE:
		s.log.Debugf("Received AMF3 data message, length: %d bytes", len(msg.Payload))
		// Handle AMF3 data messages
		return nil
	case MSG_USER_CONTROL:
		s.log.Debugf("Received user control message, length: %d bytes", len(msg.Payload))
		// Handle user control messages (stream begin, stream end, ping/pong, etc.)
		if len(msg.Payload) >= 2 {
			eventType := binary.BigEndian.Uint16(msg.Payload[0:2])
			s.log.Debugf("User control event type: %d", eventType)
			// Ping request -> respond with Ping response
			if eventType == UCM_PingRequest {
				var eventData uint32
				if len(msg.Payload) >= 6 {
					eventData = binary.BigEndian.Uint32(msg.Payload[2:6])
				}
				pong := &RTMPMessage{
					Header: MessageHeader{
						MessageTypeID:   MSG_USER_CONTROL,
						ChunkStreamID:   CSID_PROTOCOL_CONTROL,
						MessageLength:   6,
						MessageStreamID: 0,
					},
					Payload: make([]byte, 6),
				}
				binary.BigEndian.PutUint16(pong.Payload[0:2], UCM_PingResponse)
				binary.BigEndian.PutUint32(pong.Payload[2:6], eventData)
				if err := conn.WriteMessage(pong); err != nil {
					s.log.Errorf("Failed to send PingResponse: %v", err)
				} else {
					s.log.Debugf("Sent PingResponse with data=%d", eventData)
				}
			}
		}
		return nil

	case MSG_ABORT:
		s.log.Debugf("Received abort message, length: %d bytes", len(msg.Payload))
		// Handle abort messages
		return nil
	case MSG_AGGREGATE_MESSAGE:
		s.log.Debugf("Received aggregate message, length: %d bytes", len(msg.Payload))
		// Handle aggregate messages
		return nil
	default:
		// Handle unknown message types with better logging
		s.log.Debugf("Received unknown message type: %d (0x%x), length: %d bytes",
			msg.Header.MessageTypeID, msg.Header.MessageTypeID, len(msg.Payload))

		// Log first few bytes of payload for debugging
		if len(msg.Payload) > 0 {
			payloadHex := ""
			if len(msg.Payload) <= 16 {
				payloadHex = fmt.Sprintf("%x", msg.Payload)
			} else {
				payloadHex = fmt.Sprintf("%x...", msg.Payload[:16])
			}
			s.log.Debugf("Payload preview: %s", payloadHex)
		}

		// Handle specific unknown message types that appear to be media data
		switch msg.Header.MessageTypeID {
		case 20: // 0x14 - AMF0 Command Message (standard)
			s.log.Debugf("Treating message type 20 as AMF0 command message")
			return s.handleCommandMessage(conn, msg)
		case 30: // 0x1e - AMF0 Command Message (alternative)
			s.log.Debugf("Treating message type 30 as AMF0 command message")
			return s.handleCommandMessage(conn, msg)
		case 35: // 0x23 - appears to be OBS command data
			s.log.Debugf("Treating message type 35 as potential OBS command")
			// Try to parse as AMF0 command first
			if len(msg.Payload) > 0 && msg.Payload[0] == 0x02 { // AMF0 string marker
				s.log.Debugf("Message type 35 appears to be AMF0 command, attempting to parse")
				return s.handleCommandMessage(conn, msg)
			}
			// Otherwise treat as video data
			s.log.Debugf("Treating message type 35 as video data")
			return s.handleVideoMessage(conn, msg)
		case 132: // 0x84 - appears to be video data
			s.log.Debugf("Treating message type 132 as video data")
			return s.handleVideoMessage(conn, msg)
		case 195: // 0xc3 - appears to be audio data
			s.log.Debugf("Treating message type 195 as audio data")
			return s.handleAudioMessage(conn, msg)
		case 65: // 0x41 - appears to be media data
			s.log.Debugf("Treating message type 65 as video data")
			return s.handleVideoMessage(conn, msg)
		case 45: // 0x2d - appears to be media data
			s.log.Debugf("Treating message type 45 as video data")
			return s.handleVideoMessage(conn, msg)
		case 0: // 0x0 - appears to be media data
			s.log.Debugf("Treating message type 0 as audio data")
			return s.handleAudioMessage(conn, msg)
		case 80: // 0x50 - appears to be media data
			s.log.Debugf("Treating message type 80 as video data")
			return s.handleVideoMessage(conn, msg)
		case 199: // 0xC7 - appears to be media data
			s.log.Debugf("Treating message type 199 as video data")
			return s.handleVideoMessage(conn, msg)
		case 170: // 0xAA - appears to be media data
			s.log.Debugf("Treating message type 170 as video data")
			return s.handleVideoMessage(conn, msg)
		default:
			// For other unknown types, try to parse as command if it looks like AMF0
			if len(msg.Payload) > 0 && msg.Payload[0] == 0x02 { // AMF0 string marker
				s.log.Debugf("Unknown message type %d appears to be AMF0 command, attempting to parse", msg.Header.MessageTypeID)
				return s.handleCommandMessage(conn, msg)
			}
			// For message types in the media range (80-250), treat as video data
			if msg.Header.MessageTypeID >= 80 && msg.Header.MessageTypeID <= 250 {
				s.log.Debugf("Treating unknown message type %d as video data (media range)", msg.Header.MessageTypeID)
				return s.handleVideoMessage(conn, msg)
			}
			// Otherwise log and ignore
			s.log.Debugf("Ignoring unknown message type: %d", msg.Header.MessageTypeID)
			return nil
		}
	}
}

// handleCommandMessage handles AMF0 command messages
func (s *Server) handleCommandMessage(conn *RTMPConnection, msg *RTMPMessage) error {
	s.log.Debugf("Processing AMF0 command message, payload length: %d bytes", len(msg.Payload))

	// Log first few bytes for debugging
	if len(msg.Payload) > 0 {
		payloadHex := ""
		if len(msg.Payload) <= 32 {
			payloadHex = fmt.Sprintf("%x", msg.Payload)
		} else {
			payloadHex = fmt.Sprintf("%x...", msg.Payload[:32])
		}
		s.log.Debugf("Command payload preview: %s", payloadHex)
	}

	decoder := NewAMF0Decoder(msg.Payload)

	// Decode command name
	cmdName, err := decoder.DecodeString()
	if err != nil {
		s.log.Errorf("Failed to decode command name: %v, payload: %x", err, msg.Payload[:int(math.Min(float64(len(msg.Payload)), 16))])
		return fmt.Errorf("failed to decode command name: %w", err)
	}

	s.log.Infof("Received RTMP command: %s", cmdName)

	// Decode transaction ID
	transactionID, err := decoder.DecodeNumber()
	if err != nil {
		s.log.Errorf("Failed to decode transaction ID: %v", err)
		return fmt.Errorf("failed to decode transaction ID: %w", err)
	}

	s.log.Debugf("Command transaction ID: %f", transactionID)

	switch cmdName {
	case "connect":
		s.log.Debugf("Handling 'connect' command")

		// 1. Send Window Acknowledgement Size
		s.log.Debug("Sending Window Acknowledgement Size for connect")
		windowAckMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID: MSG_WINDOW_ACK_SIZE,
				ChunkStreamID: CSID_PROTOCOL_CONTROL,
				MessageLength: 4,
			},
			Payload: make([]byte, 4),
		}
		binary.BigEndian.PutUint32(windowAckMsg.Payload, 2500000)
		if err := conn.WriteMessage(windowAckMsg); err != nil {
			return fmt.Errorf("failed to send Window Acknowledgement Size: %w", err)
		}

		// 2. Send Set Peer Bandwidth
		s.log.Debug("Sending Set Peer Bandwidth for connect")
		peerBandwidthMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID: MSG_SET_PEER_BANDWIDTH,
				ChunkStreamID: CSID_PROTOCOL_CONTROL,
				MessageLength: 5,
			},
			Payload: make([]byte, 5),
		}
		binary.BigEndian.PutUint32(peerBandwidthMsg.Payload[0:4], 2500000)
		peerBandwidthMsg.Payload[4] = 2 // Dynamic
		if err := conn.WriteMessage(peerBandwidthMsg); err != nil {
			return fmt.Errorf("failed to send Set Peer Bandwidth: %w", err)
		}

		// 3. Send User Control Message (StreamBegin)
		s.log.Debug("Sending User Control Message (StreamBegin) for connect")
		streamBeginMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID: MSG_USER_CONTROL,
				ChunkStreamID: CSID_PROTOCOL_CONTROL,
				MessageLength: 6,
			},
			Payload: make([]byte, 6), // EventType (2 bytes) + StreamID (4 bytes)
		}
		binary.BigEndian.PutUint16(streamBeginMsg.Payload[0:2], UCM_StreamBegin)
		binary.BigEndian.PutUint32(streamBeginMsg.Payload[2:6], 1) // Stream ID 1
		if err := conn.WriteMessage(streamBeginMsg); err != nil {
			return fmt.Errorf("failed to send StreamBegin: %w", err)
		}

		// 4. Send Set Chunk Size (important for OBS compatibility)
		s.log.Debug("Sending Set Chunk Size for connect")
		chunkSizeMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID: MSG_SET_CHUNK_SIZE,
				ChunkStreamID: CSID_PROTOCOL_CONTROL,
				MessageLength: 4,
			},
			Payload: make([]byte, 4),
		}
		binary.BigEndian.PutUint32(chunkSizeMsg.Payload, 4096) // Set chunk size to 4096
		if err := conn.WriteMessage(chunkSizeMsg); err != nil {
			return fmt.Errorf("failed to send Set Chunk Size: %w", err)
		}

		// 5. Send _result
		s.log.Debug("Sending _result for connect")
		encoder := NewAMF0Encoder()
		encoder.EncodeString("_result")
		encoder.EncodeNumber(transactionID)

		props := []AMF0Property{
			{Key: "fmsVer", Value: "FMS/3,0,1,123"},
			{Key: "capabilities", Value: 31.0},
			{Key: "mode", Value: 1.0},
		}
		encoder.EncodeECMAArrayOrdered(props)

		info := []AMF0Property{
			{Key: "level", Value: "status"},
			{Key: "code", Value: "NetConnection.Connect.Success"},
			{Key: "description", Value: "Connection succeeded."},
			{Key: "objectEncoding", Value: 0.0},
		}
		encoder.EncodeECMAArrayOrdered(info)

		resultMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 0,
			},
			Payload: encoder.Bytes(),
		}
		if err := conn.WriteMessage(resultMsg); err != nil {
			return fmt.Errorf("failed to send connect result: %w", err)
		}
		s.log.Debugf("Connect response sequence sent successfully")
		return nil

	case "createStream":
		// Send _result with stream ID (OBS expects 1.0 as float64)
		encoder := NewAMF0Encoder()
		encoder.EncodeString("_result")
		encoder.EncodeNumber(transactionID)
		encoder.EncodeNull()
		encoder.EncodeNumber(1.0) // Stream ID (float64, always 1.0 for OBS compatibility)

		resultMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 0,
			},
			Payload: encoder.Bytes(),
		}
		return conn.WriteMessage(resultMsg)

	case "publish":
		// Skip transaction ID (null)
		if err := decoder.DecodeNull(); err != nil {
			s.log.Errorf("Failed to decode publish null transaction: %v", err)
			return fmt.Errorf("failed to decode publish null transaction: %w", err)
		}
		// Get stream name (OBS may send empty; try to parse next value if needed)
		streamName, err := decoder.DecodeString()
		if err != nil {
			s.log.Errorf("Failed to decode stream name in publish: %v", err)
			// try to recover by reading a generic AMF value and stringifying
			if val, derr := decoder.DecodeValue(); derr == nil {
				streamName = fmt.Sprintf("%v", val)
				s.log.Warnf("Recovered stream name from generic value: %s", streamName)
			} else {
				return fmt.Errorf("failed to decode stream name: %w", err)
			}
		}
		s.log.Infof("OBS publish: stream key = '%s'", streamName)
		// Log any additional AMF0 values (type, value)
		for i := 0; i < 3; i++ { // Try to decode up to 3 more values
			val, err := decoder.DecodeValue()
			if err != nil {
				s.log.Debugf("No more AMF0 values in publish command (decoded %d extra values)", i)
				break
			}
			s.log.Debugf("Extra AMF0 value in publish: %T = %v", val, val)
		}
		s.mu.Lock()
		// Overwrite or create the stream entry for this publisher
		s.streams[streamName] = &Stream{
			ID:        streamName,
			Publisher: conn,
			Players:   []*RTMPConnection{},
		}
		s.mu.Unlock()

		// Start HLS streaming
		if err := s.hls.StartStream(streamName); err != nil {
			s.log.Warnf("Failed to start HLS stream: %v", err)
		} else {
			s.log.Infof("Started HLS stream: %s", streamName)
		}

		// Send onStatus
		encoder := NewAMF0Encoder()
		encoder.EncodeString("onStatus")
		encoder.EncodeNumber(0)
		encoder.EncodeNull()
		info := []AMF0Property{
			{Key: "level", Value: "status"},
			{Key: "code", Value: "NetStream.Publish.Start"},
			{Key: "description", Value: "Stream is now published."},
		}
		encoder.EncodeECMAArrayOrdered(info)
		statusMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 1,
			},
			Payload: encoder.Bytes(),
		}
		s.log.Infof("OBS publish: sent onStatus for stream key '%s'", streamName)
		return conn.WriteMessage(statusMsg)

	case "play":
		// Skip transaction ID (null)
		if err := decoder.DecodeNull(); err != nil {
			return fmt.Errorf("failed to decode play null transaction: %w", err)
		}

		// Get stream name
		streamName, err := decoder.DecodeString()
		if err != nil {
			return fmt.Errorf("failed to decode stream name: %w", err)
		}

		s.mu.RLock()
		stream, exists := s.streams[streamName]
		s.mu.RUnlock()

		if !exists {
			return fmt.Errorf("stream not found: %s", streamName)
		}

		stream.mu.Lock()
		stream.Players = append(stream.Players, conn)
		stream.mu.Unlock()

		// Send StreamBegin (User Control) for stream ID 1
		streamBegin := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_USER_CONTROL,
				ChunkStreamID:   CSID_PROTOCOL_CONTROL,
				MessageLength:   6,
				MessageStreamID: 0,
			},
			Payload: make([]byte, 6),
		}
		binary.BigEndian.PutUint16(streamBegin.Payload[0:2], UCM_StreamBegin)
		binary.BigEndian.PutUint32(streamBegin.Payload[2:6], 1)
		if err := conn.WriteMessage(streamBegin); err != nil {
			return fmt.Errorf("failed to send StreamBegin for play: %w", err)
		}

		// Set Chunk Size to 4096 for better throughput
		setChunk := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_SET_CHUNK_SIZE,
				ChunkStreamID:   CSID_PROTOCOL_CONTROL,
				MessageLength:   4,
				MessageStreamID: 0,
			},
			Payload: make([]byte, 4),
		}
		binary.BigEndian.PutUint32(setChunk.Payload, 4096)
		if err := conn.WriteMessage(setChunk); err != nil {
			return fmt.Errorf("failed to send Set Chunk Size for play: %w", err)
		}

		// Send onStatus (NetStream.Play.Start)
		encoder := NewAMF0Encoder()
		encoder.EncodeString("onStatus")
		encoder.EncodeNumber(0) // No transaction ID
		encoder.EncodeNull()

		info := []AMF0Property{
			{Key: "level", Value: "status"},
			{Key: "code", Value: "NetStream.Play.Start"},
			{Key: "description", Value: "Stream is now playing."},
		}
		encoder.EncodeECMAArrayOrdered(info)

		statusMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 1,
			},
			Payload: encoder.Bytes(),
		}
		return conn.WriteMessage(statusMsg)

	case "releaseStream":
		s.log.Infof("Handling 'releaseStream' command")
		// Per common implementations, respond with _result
		encoder := NewAMF0Encoder()
		encoder.EncodeString("_result")
		encoder.EncodeNumber(transactionID)
		encoder.EncodeNull()
		encoder.EncodeNull()
		resultMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 0,
			},
			Payload: encoder.Bytes(),
		}
		s.log.Infof("Sent _result for releaseStream")
		return conn.WriteMessage(resultMsg)

	case "FCPublish":
		s.log.Infof("Handling 'FCPublish' command")
		// Respond with _result (transaction success)
		encoder := NewAMF0Encoder()
		encoder.EncodeString("_result")
		encoder.EncodeNumber(transactionID)
		encoder.EncodeNull()
		encoder.EncodeUndefined()
		resultMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 0,
			},
			Payload: encoder.Bytes(),
		}
		s.log.Infof("Sent _result for FCPublish")
		return conn.WriteMessage(resultMsg)

	case "deleteStream":
		s.log.Infof("Handling 'deleteStream' command")
		// Acknowledge deleteStream
		encoder := NewAMF0Encoder()
		encoder.EncodeString("onStatus")
		encoder.EncodeNumber(0)
		encoder.EncodeNull()
		info := []AMF0Property{
			{Key: "level", Value: "status"},
			{Key: "code", Value: "NetStream.Unpublish.Success"},
			{Key: "description", Value: "Stream deleted."},
		}
		encoder.EncodeECMAArrayOrdered(info)
		msg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_AMF0_COMMAND_MESSAGE,
				ChunkStreamID:   CSID_COMMAND,
				MessageLength:   uint32(len(encoder.Bytes())),
				MessageStreamID: 1,
			},
			Payload: encoder.Bytes(),
		}
		return conn.WriteMessage(msg)

	case "onFCPublish":
		s.log.Infof("Received and ignored command: %s", cmdName)
		return nil

	default:
		return fmt.Errorf("unknown command: %s", cmdName)
	}
}

// handleVideoMessage handles video messages
func (s *Server) handleVideoMessage(conn *RTMPConnection, msg *RTMPMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this connection is already a publisher for any stream
	var existingStream *Stream
	for _, stream := range s.streams {
		if stream.Publisher == conn {
			existingStream = stream
			break
		}
	}

	// If no stream exists for this connection, create one automatically
	if existingStream == nil {
		streamID := fmt.Sprintf("auto-stream-%d", time.Now().Unix())
		s.log.Infof("Auto-creating stream '%s' for video data from connection", streamID)

		existingStream = &Stream{
			ID:        streamID,
			Publisher: conn,
			Players:   []*RTMPConnection{},
		}
		s.streams[streamID] = existingStream

		// Start HLS streaming
		if err := s.hls.StartStream(streamID); err != nil {
			s.log.Warnf("Failed to start HLS stream: %v", err)
		} else {
			s.log.Infof("Started HLS stream: %s", streamID)
		}
	}

	// Forward video data to all players
	existingStream.mu.RLock()
	for _, player := range existingStream.Players {
		if err := player.WriteMessage(msg); err != nil {
			s.log.Errorf("Failed to forward video to player: %v", err)
		}
	}
	existingStream.mu.RUnlock()

	// Send video data to HLS server
	if err := s.hls.WriteVideoData(existingStream.ID, msg.Payload, msg.Header.Timestamp); err != nil {
		s.log.Debugf("Failed to write video data to HLS: %v", err)
	}

	// Send acknowledgment to keep connection alive
	if err := s.sendAcknowledgment(conn, msg.Header.MessageLength); err != nil {
		s.log.Debugf("Failed to send acknowledgment: %v", err)
	}

	s.log.Debugf("Processed video message for stream '%s', %d players", existingStream.ID, len(existingStream.Players))
	return nil
}

// handleAudioMessage handles audio messages
func (s *Server) handleAudioMessage(conn *RTMPConnection, msg *RTMPMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if this connection is already a publisher for any stream
	var existingStream *Stream
	for _, stream := range s.streams {
		if stream.Publisher == conn {
			existingStream = stream
			break
		}
	}

	// If no stream exists for this connection, create one automatically
	if existingStream == nil {
		streamID := fmt.Sprintf("auto-stream-%d", time.Now().Unix())
		s.log.Infof("Auto-creating stream '%s' for audio data from connection", streamID)

		existingStream = &Stream{
			ID:        streamID,
			Publisher: conn,
			Players:   []*RTMPConnection{},
		}
		s.streams[streamID] = existingStream
	}

	// Forward audio data to all players
	existingStream.mu.RLock()
	for _, player := range existingStream.Players {
		if err := player.WriteMessage(msg); err != nil {
			s.log.Errorf("Failed to forward audio to player: %v", err)
		}
	}
	existingStream.mu.RUnlock()

	// Send acknowledgment to keep connection alive
	if err := s.sendAcknowledgment(conn, msg.Header.MessageLength); err != nil {
		s.log.Debugf("Failed to send acknowledgment: %v", err)
	}

	s.log.Debugf("Processed audio message for stream '%s', %d players", existingStream.ID, len(existingStream.Players))
	return nil
}

// sendAcknowledgment sends an acknowledgment message to the client
func (s *Server) sendAcknowledgment(conn *RTMPConnection, bytesReceived uint32) error {
	// Track total bytes received for acknowledgment
	conn.bytesReceived += bytesReceived

	// Send ACK every 2MB or if window size is reached
	if conn.windowAckSize > 0 && conn.bytesReceived >= conn.windowAckSize {
		ackMsg := &RTMPMessage{
			Header: MessageHeader{
				MessageTypeID:   MSG_ACK,
				ChunkStreamID:   CSID_PROTOCOL_CONTROL,
				MessageLength:   4,
				MessageStreamID: 0,
			},
			Payload: make([]byte, 4),
		}

		// Set the acknowledgment sequence number
		binary.BigEndian.PutUint32(ackMsg.Payload, conn.bytesReceived)

		// Reset bytes received counter
		conn.bytesReceived = 0

		s.log.Debugf("Sending acknowledgment for %d bytes", bytesReceived)
		return conn.WriteMessage(ackMsg)
	}

	return nil
}
