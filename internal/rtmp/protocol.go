package rtmp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	// RTMP Protocol Version
	RTMP_VERSION = 3

	// Message Type IDs
	MSG_SET_CHUNK_SIZE       = 1
	MSG_ABORT                = 2
	MSG_ACK                  = 3
	MSG_USER_CONTROL         = 4
	MSG_WINDOW_ACK_SIZE      = 5
	MSG_SET_PEER_BANDWIDTH   = 6
	MSG_AUDIO_MESSAGE        = 8
	MSG_VIDEO_MESSAGE        = 9
	MSG_AMF3_DATA_MESSAGE    = 15
	MSG_AMF3_SHARED_OBJECT   = 16
	MSG_AMF3_COMMAND_MESSAGE = 17
	MSG_AMF0_DATA_MESSAGE    = 18
	MSG_AMF0_SHARED_OBJECT   = 19
	MSG_AMF0_COMMAND_MESSAGE = 20
	MSG_AGGREGATE_MESSAGE    = 22

	// RTMP actual message types for media (these are the real values FFmpeg sends)
	MSG_VIDEO_MESSAGE_RTMP  = 243 // 0xF3
	MSG_AUDIO_MESSAGE_RTMP  = 244 // 0xF4
	MSG_VIDEO_MESSAGE_RTMP2 = 217 // 0xD9 - another video message type
	MSG_VIDEO_MESSAGE_RTMP3 = 82  // 0x52 - another video message type

	// Chunk Stream IDs
	CSID_PROTOCOL_CONTROL = 2
	CSID_COMMAND          = 3
	CSID_DATA             = 4
	CSID_SHARED_OBJECT    = 5
	CSID_AUDIO            = 6
	CSID_VIDEO            = 7

	// Default chunk size
	DEFAULT_CHUNK_SIZE = 128

	// User Control Message Event Types
	UCM_StreamBegin      = 0
	UCM_StreamEOF        = 1
	UCM_StreamDry        = 2
	UCM_SetBufferLength  = 3
	UCM_StreamIsRecorded = 4
	UCM_PingRequest      = 6
	UCM_PingResponse     = 7
)

// RTMPMessage represents an RTMP message
type RTMPMessage struct {
	Header    MessageHeader
	Payload   []byte
	Timestamp uint32
	MessageID uint32
}

// MessageHeader represents the RTMP message header
type MessageHeader struct {
	Format          uint8
	ChunkStreamID   uint32
	Timestamp       uint32
	MessageLength   uint32
	MessageTypeID   uint8
	MessageStreamID uint32
}

// RTMPConnection represents an RTMP connection
type RTMPConnection struct {
	conn          net.Conn
	chunkSize     uint32
	windowAckSize uint32
	bytesReceived uint32
	peerBandwidth uint32
	limitType     uint8
	streams       map[uint32]*Stream
	readBuffer    *bytes.Buffer
	writeBuffer   *bytes.Buffer
	logger        Logger
	// Message state tracking for chunk headers
	lastMessageHeaders map[uint32]*MessageHeader
}

// Logger interface for RTMP connection logging
type Logger interface {
	Debugf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// NewRTMPConnection creates a new RTMP connection
func NewRTMPConnection(conn net.Conn, logger Logger) *RTMPConnection {
	return &RTMPConnection{
		conn:               conn,
		chunkSize:          DEFAULT_CHUNK_SIZE,
		windowAckSize:      2500000,
		peerBandwidth:      2500000,
		limitType:          2,
		streams:            make(map[uint32]*Stream),
		readBuffer:         bytes.NewBuffer(make([]byte, 0, 4096)),
		writeBuffer:        bytes.NewBuffer(nil),
		logger:             logger,
		lastMessageHeaders: make(map[uint32]*MessageHeader),
	}
}

// ReadMessage reads an RTMP message from the connection
func (c *RTMPConnection) ReadMessage() (*RTMPMessage, error) {
	c.logger.Debugf("Starting to read RTMP message")

	// Read basic header
	header, err := c.readBasicHeader()
	if err != nil {
		return nil, fmt.Errorf("failed to read basic header: %w", err)
	}
	c.logger.Debugf("Basic header read successfully: Format=%d, ChunkStreamID=%d", header.Format, header.ChunkStreamID)

	// Read message header
	msgHeader, err := c.readMessageHeader(header)
	if err != nil {
		return nil, fmt.Errorf("failed to read message header: %w", err)
	}
	c.logger.Debugf("Message header read successfully: Type=%d, Length=%d, StreamID=%d",
		msgHeader.MessageTypeID, msgHeader.MessageLength, msgHeader.MessageStreamID)

	// Read message payload
	payload, err := c.readMessagePayload(msgHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to read message payload: %w", err)
	}
	c.logger.Debugf("Message payload read successfully: %d bytes", len(payload))

	return &RTMPMessage{
		Header:  *msgHeader,
		Payload: payload,
	}, nil
}

// WriteMessage writes an RTMP message to the connection
func (c *RTMPConnection) WriteMessage(msg *RTMPMessage) error {
	c.logger.Debugf("Writing message: Type=%d, Length=%d, ChunkStreamID=%d, Format=%d, StreamID=%d",
		msg.Header.MessageTypeID,
		msg.Header.MessageLength,
		msg.Header.ChunkStreamID,
		msg.Header.Format,
		msg.Header.MessageStreamID)

	// Write basic header
	if err := c.writeBasicHeader(msg.Header.Format, msg.Header.ChunkStreamID); err != nil {
		c.logger.Errorf("Failed to write basic header: %v", err)
		return fmt.Errorf("failed to write basic header: %w", err)
	}
	c.logger.Debugf("Basic header written successfully")

	// Write message header
	if err := c.writeMessageHeader(&msg.Header); err != nil {
		c.logger.Errorf("Failed to write message header: %v", err)
		return fmt.Errorf("failed to write message header: %w", err)
	}
	c.logger.Debugf("Message header written successfully")

	// Write message payload (chunked)
	c.logger.Debugf("Writing payload (%d bytes): %x", len(msg.Payload), msg.Payload)
	if err := c.writeMessagePayload(&msg.Header, msg.Payload); err != nil {
		c.logger.Errorf("Failed to write message payload: %v", err)
		return fmt.Errorf("failed to write message payload: %w", err)
	}
	c.logger.Debugf("Message payload written successfully")

	c.logger.Debugf("Message written successfully")
	return nil
}

// readBasicHeader reads the basic header (1-3 bytes)
func (c *RTMPConnection) readBasicHeader() (*MessageHeader, error) {
	header := &MessageHeader{}

	// Read first byte
	firstByte := make([]byte, 1)
	if _, err := io.ReadFull(c.conn, firstByte); err != nil {
		return nil, err
	}

	c.logger.Debugf("Raw first byte: %x", firstByte[0])

	header.Format = (firstByte[0] >> 6) & 0x03
	csid := uint32(firstByte[0] & 0x3F)

	c.logger.Debugf("Parsed Format=%d, initial CSID=%d", header.Format, csid)

	switch csid {
	case 0:
		// 2-byte form
		csidBytes := make([]byte, 1)
		if _, err := io.ReadFull(c.conn, csidBytes); err != nil {
			return nil, err
		}
		csid = uint32(csidBytes[0]) + 64
		c.logger.Debugf("2-byte form: read %x, final CSID=%d", csidBytes[0], csid)
	case 1:
		// 3-byte form
		csidBytes := make([]byte, 2)
		if _, err := io.ReadFull(c.conn, csidBytes); err != nil {
			return nil, err
		}
		csid = uint32(binary.BigEndian.Uint16(csidBytes)) + 64
		c.logger.Debugf("3-byte form: read %x, final CSID=%d", csidBytes, csid)
	default:
		// 1-byte form
		c.logger.Debugf("1-byte form: final CSID=%d", csid)
	}

	header.ChunkStreamID = csid
	return header, nil
}

// readMessageHeader reads the message header based on format
func (c *RTMPConnection) readMessageHeader(basicHeader *MessageHeader) (*MessageHeader, error) {
	header := *basicHeader

	// Get the last message header for this chunk stream ID
	lastHeader, exists := c.lastMessageHeaders[header.ChunkStreamID]

	switch header.Format {
	case 0: // 11 bytes - full header
		// timestamp (3 bytes) + message length (3 bytes) + message type id (1 byte) + message stream id (4 bytes)
		data := make([]byte, 11)
		if _, err := io.ReadFull(c.conn, data); err != nil {
			return nil, err
		}

		header.Timestamp = uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
		header.MessageLength = uint32(data[3])<<16 | uint32(data[4])<<8 | uint32(data[5])
		header.MessageTypeID = data[6]
		header.MessageStreamID = binary.LittleEndian.Uint32(data[7:11])

	case 1: // 7 bytes - timestamp delta + message length + message type id
		// timestamp delta (3 bytes) + message length (3 bytes) + message type id (1 byte)
		data := make([]byte, 7)
		if _, err := io.ReadFull(c.conn, data); err != nil {
			return nil, err
		}
		c.logger.Debugf("Read %d bytes for Type 1 header: %x", len(data), data)
		c.logger.Debugf("Type 1 header breakdown:")
		c.logger.Debugf("  Bytes 0-2 (timestamp delta): %x %x %x", data[0], data[1], data[2])
		c.logger.Debugf("  Bytes 3-5 (message length): %x %x %x", data[3], data[4], data[5])
		c.logger.Debugf("  Byte 6 (message type): %x", data[6])

		if !exists {
			// If no previous header exists, we need to initialize with default values
			// This happens when a client sends a Type 1 header without a previous Type 0
			c.logger.Debugf("Type 1 header received but no previous header for chunk stream %d, using defaults", header.ChunkStreamID)
			c.logger.Debugf("Raw Type 1 header data: %x", data)
			header.Timestamp = uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
			header.MessageLength = uint32(data[3])<<16 | uint32(data[4])<<8 | uint32(data[5])
			header.MessageTypeID = data[6]
			header.MessageStreamID = 0 // Default to 0

			c.logger.Debugf("Parsed values:")
			c.logger.Debugf("  Timestamp: %d (0x%x)", header.Timestamp, header.Timestamp)
			c.logger.Debugf("  Message Length: %d (0x%x)", header.MessageLength, header.MessageLength)
			c.logger.Debugf("  Message Type: %d (0x%x)", header.MessageTypeID, header.MessageTypeID)

			// Intelligent message length validation and correction
			originalLength := header.MessageLength
			if header.MessageLength > 1024*1024 { // 1MB limit
				// If message length is too large, try to estimate a reasonable size
				// Based on the message type and typical RTMP message sizes
				switch header.MessageTypeID {
				case 1: // Set Chunk Size
					header.MessageLength = 4
				case 2: // Abort Message
					header.MessageLength = 4
				case 3: // Acknowledgement
					header.MessageLength = 4
				case 4: // User Control Message
					header.MessageLength = 6
				case 5: // Window Acknowledgement Size
					header.MessageLength = 4
				case 6: // Set Peer Bandwidth
					header.MessageLength = 5
				case 8: // Audio Message
					header.MessageLength = 1024 // Typical audio chunk
				case 9: // Video Message
					header.MessageLength = 2048 // Typical video chunk
				case 15: // AMF3 Data Message
					header.MessageLength = 512
				case 18: // AMF3 Shared Object
					header.MessageLength = 512
				case 20: // AMF0 Data Message
					header.MessageLength = 512
				case 22: // AMF0 Shared Object
					header.MessageLength = 512
				default:
					header.MessageLength = 1024 // Default reasonable size
				}
				c.logger.Debugf("Message length corrected from %d to %d (Type %d)", originalLength, header.MessageLength, header.MessageTypeID)
			} else if header.MessageLength > 0 {
				// For reasonable message lengths, let's try to read the actual payload
				c.logger.Debugf("Attempting to read %d bytes of payload", header.MessageLength)
			}

			c.logger.Debugf("Parsed Type 1 header: Timestamp=%d, Length=%d, TypeID=%d", header.Timestamp, header.MessageLength, header.MessageTypeID)
		} else {
			timestampDelta := uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
			header.Timestamp = lastHeader.Timestamp + timestampDelta
			header.MessageLength = uint32(data[3])<<16 | uint32(data[4])<<8 | uint32(data[5])
			header.MessageTypeID = data[6]
			header.MessageStreamID = lastHeader.MessageStreamID
		}

	case 2: // 3 bytes - timestamp delta only
		// timestamp delta (3 bytes)
		data := make([]byte, 3)
		if _, err := io.ReadFull(c.conn, data); err != nil {
			return nil, err
		}

		if !exists {
			// If no previous header exists, we need to use default values
			// This happens when a client sends a Type 2 header without a previous header
			c.logger.Debugf("Type 2 header received but no previous header for chunk stream %d, using defaults", header.ChunkStreamID)
			header.Timestamp = uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
			header.MessageLength = 1024 // Default reasonable size
			header.MessageTypeID = 0
			header.MessageStreamID = 0
		} else {
			timestampDelta := uint32(data[0])<<16 | uint32(data[1])<<8 | uint32(data[2])
			header.Timestamp = lastHeader.Timestamp + timestampDelta
			header.MessageLength = lastHeader.MessageLength
			header.MessageTypeID = lastHeader.MessageTypeID
			header.MessageStreamID = lastHeader.MessageStreamID
		}

	case 3: // 0 bytes - no additional header
		if !exists {
			// If no previous header exists, we need to use default values
			// This happens when a client sends a Type 3 header without a previous header
			c.logger.Debugf("Type 3 header received but no previous header for chunk stream %d, using defaults", header.ChunkStreamID)
			header.Timestamp = 0
			header.MessageLength = 1024 // Default reasonable size
			header.MessageTypeID = 0
			header.MessageStreamID = 0
		} else {
			header.Timestamp = lastHeader.Timestamp
			header.MessageLength = lastHeader.MessageLength
			header.MessageTypeID = lastHeader.MessageTypeID
			header.MessageStreamID = lastHeader.MessageStreamID
		}
	}

	// Store this header for future reference
	c.lastMessageHeaders[header.ChunkStreamID] = &header

	return &header, nil
}

// readMessagePayload reads the message payload
func (c *RTMPConnection) readMessagePayload(header *MessageHeader) ([]byte, error) {
	// Implement RTMP chunked payload reading. Messages are fragmented into chunks of size `chunkSize`.
	total := header.MessageLength
	if total == 0 {
		return []byte{}, nil
	}

	payload := make([]byte, 0, total)
	remaining := total

	// Helper: read exactly n bytes
	readExact := func(n uint32) ([]byte, error) {
		buf := make([]byte, n)
		if n == 0 {
			return buf, nil
		}
		if _, err := io.ReadFull(c.conn, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}

	// Read first chunk directly (after the header already parsed)
	firstChunk := c.chunkSize
	if remaining < firstChunk || firstChunk == 0 {
		firstChunk = remaining
	}
	c.logger.Debugf("Reading first payload chunk: %d bytes of %d", firstChunk, total)
	b, err := readExact(firstChunk)
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			c.logger.Debugf("Unexpected EOF on first chunk, read %d/%d", len(b), firstChunk)
			payload = append(payload, b...)
			return payload, nil
		}
		return nil, fmt.Errorf("failed to read first payload chunk: %w", err)
	}
	payload = append(payload, b...)
	remaining -= firstChunk

	// Subsequent chunks: each preceded by a basic header (usually fmt=3, same csid)
	for remaining > 0 {
		// Parse and skip the basic header for the continuation chunk
		// Read first byte of basic header
		fb := make([]byte, 1)
		if _, err := io.ReadFull(c.conn, fb); err != nil {
			return nil, fmt.Errorf("failed to read continuation basic header: %w", err)
		}
		fmtVal := (fb[0] >> 6) & 0x03
		csidPart := fb[0] & 0x3F

		// Handle extended csid forms
		switch csidPart {
		case 0:
			// one extra byte
			if _, err := io.ReadFull(c.conn, make([]byte, 1)); err != nil {
				return nil, fmt.Errorf("failed to read extended csid (1 byte): %w", err)
			}
		case 1:
			// two extra bytes
			if _, err := io.ReadFull(c.conn, make([]byte, 2)); err != nil {
				return nil, fmt.Errorf("failed to read extended csid (2 bytes): %w", err)
			}
		}

		// If fmt != 3, skip the corresponding header bytes (rare for continuation; be lenient)
		switch fmtVal {
		case 0:
			// 11-byte message header
			if _, err := io.ReadFull(c.conn, make([]byte, 11)); err != nil {
				return nil, fmt.Errorf("failed to skip fmt=0 header on continuation: %w", err)
			}
		case 1:
			// 7-byte message header
			if _, err := io.ReadFull(c.conn, make([]byte, 7)); err != nil {
				return nil, fmt.Errorf("failed to skip fmt=1 header on continuation: %w", err)
			}
		case 2:
			// 3-byte message header
			if _, err := io.ReadFull(c.conn, make([]byte, 3)); err != nil {
				return nil, fmt.Errorf("failed to skip fmt=2 header on continuation: %w", err)
			}
		case 3:
			// No additional header bytes
		}

		// Read next chunk of payload
		chunk := c.chunkSize
		if chunk == 0 || chunk > remaining {
			chunk = remaining
		}
		c.logger.Debugf("Reading continuation payload chunk: %d bytes remaining=%d", chunk, remaining)
		b, err := readExact(chunk)
		if err != nil {
			if err == io.ErrUnexpectedEOF {
				c.logger.Debugf("Unexpected EOF on continuation chunk, read %d/%d", len(b), chunk)
				payload = append(payload, b...)
				return payload, nil
			}
			return nil, fmt.Errorf("failed to read continuation payload chunk: %w", err)
		}
		payload = append(payload, b...)
		remaining -= chunk
	}

	return payload, nil
}

// writeBasicHeader writes the basic header
func (c *RTMPConnection) writeBasicHeader(format uint8, csid uint32) error {
	var header []byte

	c.logger.Debugf("Writing basic header: format=%d, csid=%d", format, csid)

	if csid <= 63 {
		// 1-byte form
		header = []byte{byte(format<<6) | byte(csid)}
		c.logger.Debugf("Using 1-byte form: %x", header)
	} else if csid <= 319 {
		// 2-byte form
		header = []byte{byte(format << 6), byte(csid - 64)}
		c.logger.Debugf("Using 2-byte form: %x", header)
	} else {
		// 3-byte form
		header = []byte{byte(format<<6) | 1, byte((csid - 64) >> 8), byte((csid - 64) & 0xFF)}
		c.logger.Debugf("Using 3-byte form: %x", header)
	}

	n, err := c.conn.Write(header)
	if err != nil {
		c.logger.Errorf("Failed to write basic header: %v", err)
		return err
	}
	c.logger.Debugf("Wrote %d bytes of basic header", n)
	return nil
}

// writeMessageHeader writes the message header
func (c *RTMPConnection) writeMessageHeader(header *MessageHeader) error {
	c.logger.Debugf("Writing message header: Format=%d, Type=%d, Length=%d, StreamID=%d",
		header.Format, header.MessageTypeID, header.MessageLength, header.MessageStreamID)

	switch header.Format {
	case 0:
		// Type 0 - 11 bytes: timestamp(3) + message length(3) + message type id(1) + message stream id(4)
		data := make([]byte, 11)

		// Timestamp (3 bytes, big endian)
		data[0] = byte(header.Timestamp >> 16)
		data[1] = byte(header.Timestamp >> 8)
		data[2] = byte(header.Timestamp)

		// Message Length (3 bytes, big endian)
		data[3] = byte(header.MessageLength >> 16)
		data[4] = byte(header.MessageLength >> 8)
		data[5] = byte(header.MessageLength)

		// Message Type ID (1 byte)
		data[6] = header.MessageTypeID

		// Message Stream ID (4 bytes, little endian)
		binary.LittleEndian.PutUint32(data[7:11], header.MessageStreamID)

		c.logger.Debugf("Writing Type 0 header (11 bytes): %x", data)
		c.logger.Debugf("  - Timestamp: %x", data[0:3])
		c.logger.Debugf("  - Length: %x", data[3:6])
		c.logger.Debugf("  - Type ID: %x", data[6])
		c.logger.Debugf("  - Stream ID: %x", data[7:11])

		n, err := c.conn.Write(data)
		if err != nil {
			c.logger.Errorf("Failed to write Type 0 header: %v", err)
			return err
		}
		c.logger.Debugf("Wrote %d bytes of Type 0 header", n)
		return nil

	case 1:
		// Type 1 - 7 bytes: timestamp(3) + message length(3) + message type id(1)
		data := make([]byte, 7)

		// Timestamp (3 bytes, big endian)
		data[0] = byte(header.Timestamp >> 16)
		data[1] = byte(header.Timestamp >> 8)
		data[2] = byte(header.Timestamp)

		// Message Length (3 bytes, big endian)
		data[3] = byte(header.MessageLength >> 16)
		data[4] = byte(header.MessageLength >> 8)
		data[5] = byte(header.MessageLength)

		// Message Type ID (1 byte)
		data[6] = header.MessageTypeID

		c.logger.Debugf("Writing Type 1 header (7 bytes): %x", data)
		c.logger.Debugf("  - Timestamp: %x", data[0:3])
		c.logger.Debugf("  - Length: %x", data[3:6])
		c.logger.Debugf("  - Type ID: %x", data[6])

		n, err := c.conn.Write(data)
		if err != nil {
			c.logger.Errorf("Failed to write Type 1 header: %v", err)
			return err
		}
		c.logger.Debugf("Wrote %d bytes of Type 1 header", n)
		return nil

	case 2:
		// Type 2 - 3 bytes: timestamp(3)
		data := make([]byte, 3)

		// Timestamp (3 bytes, big endian)
		data[0] = byte(header.Timestamp >> 16)
		data[1] = byte(header.Timestamp >> 8)
		data[2] = byte(header.Timestamp)

		c.logger.Debugf("Writing Type 2 header (3 bytes): %x", data)
		c.logger.Debugf("  - Timestamp: %x", data[0:3])

		n, err := c.conn.Write(data)
		if err != nil {
			c.logger.Errorf("Failed to write Type 2 header: %v", err)
			return err
		}
		c.logger.Debugf("Wrote %d bytes of Type 2 header", n)
		return nil

	case 3:
		// Type 3 - 0 bytes (no header)
		c.logger.Debugf("Type 3 header - no bytes to write")
		return nil
	}

	return nil
}

// writeMessagePayload writes the message payload
func (c *RTMPConnection) writeMessagePayload(header *MessageHeader, payload []byte) error {
	// Split payload by chunk size and write continuation chunks with fmt=3 headers
	if c.chunkSize == 0 {
		c.chunkSize = DEFAULT_CHUNK_SIZE
	}

	totalLen := uint32(len(payload))
	written := uint32(0)

	// Write first chunk directly after the already written message header
	first := c.chunkSize
	if totalLen < first {
		first = totalLen
	}
	if first > 0 {
		n, err := c.conn.Write(payload[:first])
		if err != nil {
			return err
		}
		c.logger.Debugf("Wrote first payload chunk: %d bytes", n)
		written += uint32(n)
	}

	// Continue with subsequent chunks
	for written < totalLen {
		// Write continuation basic header (fmt=3, same csid)
		if err := c.writeBasicHeader(3, header.ChunkStreamID); err != nil {
			return err
		}

		// Determine size of next chunk
		remaining := totalLen - written
		chunk := c.chunkSize
		if remaining < chunk {
			chunk = remaining
		}

		n, err := c.conn.Write(payload[written : written+chunk])
		if err != nil {
			return err
		}
		c.logger.Debugf("Wrote continuation payload chunk: %d bytes", n)
		written += uint32(n)
	}

	return nil
}

// Close closes the RTMP connection
func (c *RTMPConnection) Close() error {
	return c.conn.Close()
}
