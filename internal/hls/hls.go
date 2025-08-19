package hls

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"paitec-rtmp/internal/logger"
)

// HLSServer represents an HLS server
type HLSServer struct {
	outputDir           string
	segmentDuration     time.Duration
	segmentsPerPlaylist int
	mu                  sync.RWMutex
	streams             map[string]*HLSStream
	log                 *logger.Logger
}

// HLSStream represents an HLS stream
type HLSStream struct {
	Name           string
	Segments       []*HLSSegment
	CurrentSegment *HLSSegment
	SegmentCounter int
	mu             sync.RWMutex
}

// HLSSegment represents an HLS segment
type HLSSegment struct {
	Index    int
	Path     string
	Duration float64
	Size     int64
	Created  time.Time
}

// NewHLSServer creates a new HLS server
func NewHLSServer(outputDir string, segmentDuration time.Duration, segmentsPerPlaylist int) *HLSServer {
	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		panic(fmt.Sprintf("Failed to create HLS output directory: %v", err))
	}

	return &HLSServer{
		outputDir:           outputDir,
		segmentDuration:     segmentDuration,
		segmentsPerPlaylist: segmentsPerPlaylist,
		streams:             make(map[string]*HLSStream),
		log:                 logger.GetLogger(),
	}
}

// StartStream starts HLS streaming for a given stream name using FFmpeg
func (s *HLSServer) StartStream(streamName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create stream directory
	streamDir := filepath.Join(s.outputDir, streamName)
	if err := os.MkdirAll(streamDir, 0755); err != nil {
		return fmt.Errorf("failed to create stream directory: %v", err)
	}

	hlsStream := &HLSStream{
		Name:           streamName,
		Segments:       make([]*HLSSegment, 0),
		SegmentCounter: 0,
	}

	s.streams[streamName] = hlsStream

	// FFmpeg HLS conversion disabled - streams will be available for manual HLS conversion or direct RTMP playback

	s.log.Infof("Started HLS stream: %s", streamName)
	return nil
}

// StopStream stops HLS streaming for a given stream name
func (s *HLSServer) StopStream(streamName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.streams[streamName]; exists {
		// Clean up segments
		streamDir := filepath.Join(s.outputDir, streamName)
		if err := os.RemoveAll(streamDir); err != nil {
			s.log.Warnf("Failed to clean up stream directory: %v", err)
		}
		delete(s.streams, streamName)
		s.log.Infof("Stopped HLS stream: %s", streamName)
	}
	return nil
}

// WriteVideoData writes video data to HLS segments using FFmpeg conversion
func (s *HLSServer) WriteVideoData(streamName string, data []byte, timestamp uint32) error {
	s.mu.RLock()
	stream, exists := s.streams[streamName]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("stream not found: %s", streamName)
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()

	// For now, just increment counter - FFmpeg will handle the actual HLS generation
	stream.SegmentCounter++

	// Log that we're receiving data (for debugging)
	if stream.SegmentCounter%50 == 0 {
		s.log.Debugf("Received %d video packets for stream %s", stream.SegmentCounter, streamName)
	}

	return nil
}

// startFFmpegHLS starts FFmpeg process to convert RTMP stream to HLS
func (s *HLSServer) startFFmpegHLS(streamName, streamDir string) {
	// Wait a bit for RTMP stream to be established
	time.Sleep(2 * time.Second)

	rtmpURL := fmt.Sprintf("rtmp://localhost:1935/live/%s", streamName)
	playlistPath := filepath.Join(streamDir, fmt.Sprintf("%s.m3u8", streamName))

	// FFmpeg command to convert RTMP to HLS
	cmd := exec.Command("ffmpeg",
		"-i", rtmpURL,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-c:a", "aac",
		"-f", "hls",
		"-hls_time", "10",
		"-hls_list_size", "10",
		"-hls_flags", "delete_segments",
		playlistPath,
	)

	s.log.Infof("Starting FFmpeg HLS conversion for stream %s", streamName)
	s.log.Debugf("FFmpeg command: %s", cmd.String())

	if err := cmd.Start(); err != nil {
		s.log.Errorf("Failed to start FFmpeg for stream %s: %v", streamName, err)
		return
	}

	// Wait for FFmpeg to finish (or be killed)
	if err := cmd.Wait(); err != nil {
		s.log.Warnf("FFmpeg process for stream %s ended: %v", streamName, err)
	}
}

// createNewSegment creates a new HLS segment
func (s *HLSServer) createNewSegment(stream *HLSStream) error {
	// Close current segment if exists
	if stream.CurrentSegment != nil {
		stream.Segments = append(stream.Segments, stream.CurrentSegment)

		// Remove old segments if we exceed the limit
		if len(stream.Segments) > s.segmentsPerPlaylist {
			oldSegment := stream.Segments[0]
			stream.Segments = stream.Segments[1:]

			// Remove old segment file
			oldPath := filepath.Join(s.outputDir, stream.Name, oldSegment.Path)
			if err := os.Remove(oldPath); err != nil {
				s.log.Warnf("Failed to remove old segment: %v", err)
			}
		}
	}

	// Create new segment
	segmentName := fmt.Sprintf("segment_%d.ts", stream.SegmentCounter)
	segmentPath := filepath.Join(s.outputDir, stream.Name, segmentName)

	// Create empty segment file
	file, err := os.Create(segmentPath)
	if err != nil {
		return fmt.Errorf("failed to create segment file: %v", err)
	}
	file.Close()

	stream.CurrentSegment = &HLSSegment{
		Index:    stream.SegmentCounter,
		Path:     segmentName,
		Duration: s.segmentDuration.Seconds(),
		Size:     0,
		Created:  time.Now(),
	}

	stream.SegmentCounter = 0
	return nil
}

// writeFLVHeader writes FLV header to a file
func (s *HLSServer) writeFLVHeader(file *os.File) error {
	// FLV header: FLV version 1, has video and audio
	header := []byte{
		0x46, 0x4C, 0x56, 0x01, // FLV
		0x05,                   // Version 1, has video and audio
		0x00, 0x00, 0x00, 0x09, // Header length
	}

	_, err := file.Write(header)
	return err
}

// GeneratePlaylist generates an M3U8 playlist for a stream
func (s *HLSServer) GeneratePlaylist(streamName string) (string, error) {
	s.mu.RLock()
	stream, exists := s.streams[streamName]
	s.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("stream not found: %s", streamName)
	}

	stream.mu.RLock()
	defer stream.mu.RUnlock()

	playlist := "#EXTM3U\n"
	playlist += "#EXT-X-VERSION:3\n"
	playlist += fmt.Sprintf("#EXT-X-TARGETDURATION:%.0f\n", s.segmentDuration.Seconds())
	playlist += "#EXT-X-MEDIA-SEQUENCE:0\n"

	// Add segments
	for _, segment := range stream.Segments {
		playlist += fmt.Sprintf("#EXTINF:%.3f,\n", segment.Duration)
		playlist += segment.Path + "\n"
	}

	// Add current segment if it has data
	if stream.CurrentSegment != nil && stream.CurrentSegment.Size > 0 {
		playlist += fmt.Sprintf("#EXTINF:%.3f,\n", stream.CurrentSegment.Duration)
		playlist += stream.CurrentSegment.Path + "\n"
	}

	// Don't add #EXT-X-ENDLIST for live streams
	// playlist += "#EXT-X-ENDLIST\n"
	return playlist, nil
}

// GetStreamInfo returns information about a stream
func (s *HLSServer) GetStreamInfo(streamName string) (*HLSStream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stream, exists := s.streams[streamName]
	if !exists {
		return nil, fmt.Errorf("stream not found: %s", streamName)
	}

	return stream, nil
}

// GetStreams returns all active streams
func (s *HLSServer) GetStreams() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	streams := make([]string, 0, len(s.streams))
	for name := range s.streams {
		streams = append(streams, name)
	}
	return streams
}

// GetSegmentPath returns the full path to a segment
func (s *HLSServer) GetSegmentPath(streamName, segmentName string) string {
	return filepath.Join(s.outputDir, streamName, segmentName)
}

// GetOutputDir returns the HLS output directory
func (s *HLSServer) GetOutputDir() string {
	return s.outputDir
}
