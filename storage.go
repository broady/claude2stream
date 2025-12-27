package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ahimsalabs/durable-streams-go/durablestream"
	"github.com/fsnotify/fsnotify"
)

var (
	ErrReadOnly     = errors.New("storage is read-only")
	ErrStreamNotFound = errors.New("stream not found")
)

// ClaudeStorage implements durablestream.Storage as a read-only view
// over Claude conversation JSONL files.
//
// Special streams:
//   - "_history": ~/.claude/history.jsonl (command history with session links)
//
// All other stream IDs are treated as conversation UUIDs and resolved
// from ~/.claude/projects/**/{id}.jsonl
type ClaudeStorage struct {
	claudeDir   string // ~/.claude
	projectsDir string // ~/.claude/projects

	mu          sync.RWMutex
	watcher     *fsnotify.Watcher
	subscribers map[string][]chan durablestream.Offset // streamID -> channels
	fileIndex   map[string]string                      // streamID -> file path
}

// NewClaudeStorage creates a storage backed by the given Claude directory
// (typically ~/.claude). It watches for file changes and indexes conversations.
func NewClaudeStorage(claudeDir string) (*ClaudeStorage, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}

	s := &ClaudeStorage{
		claudeDir:   claudeDir,
		projectsDir: filepath.Join(claudeDir, "projects"),
		watcher:     watcher,
		subscribers: make(map[string][]chan durablestream.Offset),
		fileIndex:   make(map[string]string),
	}

	// Register special streams
	historyPath := filepath.Join(claudeDir, "history.jsonl")
	s.fileIndex["_history"] = historyPath
	watcher.Add(claudeDir) // watch for history.jsonl changes

	// Build initial index and watch project directories
	if err := s.indexFiles(); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("index files: %w", err)
	}

	go s.watchLoop()

	return s, nil
}

func (s *ClaudeStorage) indexFiles() error {
	return filepath.WalkDir(s.projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			s.watcher.Add(path)
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			streamID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			s.mu.Lock()
			s.fileIndex[streamID] = path
			s.mu.Unlock()
		}
		return nil
	})
}

func (s *ClaudeStorage) watchLoop() {
	historyPath := filepath.Join(s.claudeDir, "history.jsonl")

	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				if strings.HasSuffix(event.Name, ".jsonl") {
					var streamID string
					if event.Name == historyPath {
						streamID = "_history"
					} else {
						streamID = strings.TrimSuffix(filepath.Base(event.Name), ".jsonl")
						s.mu.Lock()
						s.fileIndex[streamID] = event.Name
						s.mu.Unlock()
					}

					// Get new tail offset
					tail, _ := s.getTailOffset(event.Name)

					// Notify subscribers
					s.mu.RLock()
					for _, ch := range s.subscribers[streamID] {
						select {
						case ch <- tail:
						default: // don't block
						}
					}
					s.mu.RUnlock()
				}
			}
		case _, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (s *ClaudeStorage) getTailOffset(path string) (durablestream.Offset, error) {
	info, err := os.Stat(path)
	if err != nil {
		return durablestream.ZeroOffset, err
	}
	return offsetFromInt(info.Size()), nil
}

func offsetFromInt(n int64) durablestream.Offset {
	return durablestream.Offset(fmt.Sprintf("%020d", n))
}

func offsetToInt(o durablestream.Offset) int64 {
	if o == durablestream.ZeroOffset || string(o) == "-1" {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimLeft(string(o), "0"), 10, 64)
	return n
}

func (s *ClaudeStorage) getPath(streamID string) (string, error) {
	// Strip leading slash from URL path
	streamID = strings.TrimPrefix(streamID, "/")

	s.mu.RLock()
	path, ok := s.fileIndex[streamID]
	s.mu.RUnlock()

	if ok {
		return path, nil
	}

	// Try to find it in projects
	pattern := filepath.Join(s.projectsDir, "**", streamID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err == nil && len(matches) > 0 {
		s.mu.Lock()
		s.fileIndex[streamID] = matches[0]
		s.mu.Unlock()
		return matches[0], nil
	}

	// Deeper search
	var found string
	filepath.WalkDir(s.projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(path) == streamID+".jsonl" {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if found != "" {
		s.mu.Lock()
		s.fileIndex[streamID] = found
		s.mu.Unlock()
		return found, nil
	}

	return "", ErrStreamNotFound
}

// Create is not supported (read-only storage).
func (s *ClaudeStorage) Create(ctx context.Context, streamID string, cfg durablestream.StreamConfig) (bool, error) {
	return false, ErrReadOnly
}

// Append is not supported (read-only storage).
func (s *ClaudeStorage) Append(ctx context.Context, streamID string, data []byte, seq string) (durablestream.Offset, error) {
	return durablestream.ZeroOffset, ErrReadOnly
}

// AppendFrom is not supported (read-only storage).
func (s *ClaudeStorage) AppendFrom(ctx context.Context, streamID string, r io.Reader, seq string) (durablestream.Offset, error) {
	return durablestream.ZeroOffset, ErrReadOnly
}

// Delete is not supported (read-only storage).
func (s *ClaudeStorage) Delete(ctx context.Context, streamID string) error {
	return ErrReadOnly
}

// Head returns stream metadata.
func (s *ClaudeStorage) Head(ctx context.Context, streamID string) (*durablestream.StreamInfo, error) {
	path, err := s.getPath(streamID)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	return &durablestream.StreamInfo{
		ContentType: "application/json",
		NextOffset:  offsetFromInt(info.Size()),
	}, nil
}

// Read returns messages from offset.
func (s *ClaudeStorage) Read(ctx context.Context, streamID string, offset durablestream.Offset, limit int) (*durablestream.ReadResult, error) {
	path, err := s.getPath(streamID)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	startOffset := offsetToInt(offset)
	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	tailOffset := offsetFromInt(info.Size())

	var messages []durablestream.StoredMessage
	currentOffset := startOffset
	bytesRead := 0

	scanner := bufio.NewScanner(f)
	// Handle potentially large lines (some history entries can be >1MB)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 16*1024*1024) // 16MB max line size

	for scanner.Scan() {
		line := scanner.Bytes()
		lineLen := int64(len(line) + 1) // +1 for newline

		if bytesRead+len(line) > limit && len(messages) > 0 {
			break
		}

		// Validate JSON before including - skip malformed lines
		// (can happen if file is being written while we read)
		if !json.Valid(line) {
			currentOffset += lineLen
			continue
		}

		// For JSON mode, store raw JSON object (handler will format as array)
		data := make([]byte, len(line))
		copy(data, line)

		currentOffset += lineLen
		messages = append(messages, durablestream.StoredMessage{
			Data:   data,
			Offset: offsetFromInt(currentOffset),
		})
		bytesRead += len(line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	nextOffset := offsetFromInt(currentOffset)
	if len(messages) == 0 {
		nextOffset = offset
	}

	return &durablestream.ReadResult{
		Messages:   messages,
		NextOffset: nextOffset,
		TailOffset: tailOffset,
	}, nil
}

// Subscribe returns a channel notified when new data arrives.
func (s *ClaudeStorage) Subscribe(ctx context.Context, streamID string, offset durablestream.Offset) (<-chan durablestream.Offset, error) {
	// Strip leading slash to match watchLoop's streamID format
	streamID = strings.TrimPrefix(streamID, "/")

	_, err := s.getPath(streamID)
	if err != nil {
		return nil, err
	}

	ch := make(chan durablestream.Offset, 1)

	s.mu.Lock()
	s.subscribers[streamID] = append(s.subscribers[streamID], ch)
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		subs := s.subscribers[streamID]
		for i, c := range subs {
			if c == ch {
				s.subscribers[streamID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(ch)
	}()

	return ch, nil
}

func (s *ClaudeStorage) Close() error {
	return s.watcher.Close()
}
