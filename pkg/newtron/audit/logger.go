package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// maxScanLine bounds the per-line buffer when reading the audit log. Events now
// carry a request body (capped at 1 MiB by the middleware) plus a change-set,
// so a single JSON line can far exceed bufio.Scanner's 64 KiB default — without
// this, such a line would error mid-scan and truncate the read. 8 MiB leaves
// generous headroom above the middleware's own caps.
const maxScanLine = 8 << 20

// Logger defines the interface for audit logging backends
type Logger interface {
	Log(event *Event) error
	Query(filter Filter) ([]*Event, error)
	Close() error
}

// FileLogger logs audit events to a JSON-lines file
type FileLogger struct {
	path      string
	file      *os.File
	encoder   *json.Encoder
	mu        sync.RWMutex
	rotation  RotationConfig
	integrity bool
	lastHash  string
}

// RotationConfig configures log file rotation
type RotationConfig struct {
	MaxSize    int64 // Max file size in bytes before rotation
	MaxBackups int   // Max number of old files to retain
}

// NewFileLogger creates a new file-based audit logger
func NewFileLogger(path string, rotation RotationConfig) (*FileLogger, error) {
	return newFileLogger(path, rotation, false)
}

// NewFileLoggerWithIntegrity creates a file-based audit logger with
// hash-chain integrity (auth-design.md L6). Each event's ID is set
// to SHA256(prev_hash || canonical_json_of_event) before append;
// PrevHash links to the previous entry. Operators run audit.Verify
// on the file periodically to detect tampering.
//
// On startup, the chain head is recovered from the file's last
// well-formed entry's ID. The chain therefore continues across
// server restarts — operators see one verifiable chain end to end
// over multiple lifecycles.
func NewFileLoggerWithIntegrity(path string, rotation RotationConfig) (*FileLogger, error) {
	return newFileLogger(path, rotation, true)
}

func newFileLogger(path string, rotation RotationConfig, integrity bool) (*FileLogger, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}

	l := &FileLogger{
		path:      path,
		file:      file,
		encoder:   json.NewEncoder(file),
		rotation:  rotation,
		integrity: integrity,
	}
	if integrity {
		head, err := readChainHead(path)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("recovering audit chain head: %w", err)
		}
		l.lastHash = head
	}
	return l, nil
}

// Log writes an audit event to the log file. When integrity is on
// (auth-design.md L6), Log populates event.PrevHash with the running
// chain head and event.ID with SHA256(prev_hash || canonical JSON
// of the event) before append; the chain head advances so the next
// entry links to this one.
func (l *FileLogger) Log(event *Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check if rotation needed
	if l.rotation.MaxSize > 0 {
		if info, err := l.file.Stat(); err == nil {
			if info.Size() >= l.rotation.MaxSize {
				if err := l.rotate(); err != nil {
					return fmt.Errorf("rotating audit log: %w", err)
				}
			}
		}
	}

	if l.integrity {
		event.PrevHash = l.lastHash
		content, err := marshalCanonical(event)
		if err != nil {
			return fmt.Errorf("hashing audit event: %w", err)
		}
		event.ID = computeEventHash(l.lastHash, content)
		l.lastHash = event.ID
	}

	return l.encoder.Encode(event)
}

// FindByID returns the single event whose hash-chain ID matches id, or nil if
// no such event exists in the log. Unlike Query, it returns the full event
// including RequestBody — the per-event detail endpoint's reason to exist.
// Scans the append-only log; on typical sizes this is cheap, and a detail
// fetch is one-per-click, not a polling loop.
func (l *FileLogger) FindByID(id string) (*Event, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	file, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanLine)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			util.Logger.Warnf("audit: skipping malformed log entry at line %d: %v", lineNum, err)
			continue
		}
		if event.ID == id {
			return &event, nil
		}
	}
	return nil, scanner.Err()
}

// Query searches for events matching the filter
func (l *FileLogger) Query(filter Filter) ([]*Event, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	file, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Event{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var events []*Event
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanLine)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			util.Logger.Warnf("audit: skipping malformed log entry at line %d: %v", lineNum, err)
			continue
		}

		if l.matchesFilter(&event, filter) {
			events = append(events, &event)
		}
	}

	// Order before paging so Offset/Limit start from the chosen end.
	// Default is newest-first; OrderOldestFirst keeps chronological order.
	// The on-disk file is append order (oldest→newest); reversing here is
	// a presentation concern and does not touch the hash chain (the
	// prev_hash links live in the event data and are verified in build
	// order by the integrity walk, independent of read order).
	if filter.Order != OrderOldestFirst {
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
	}

	// Apply offset and limit
	if filter.Offset > 0 {
		if filter.Offset >= len(events) {
			events = nil
		} else {
			events = events[filter.Offset:]
		}
	}
	if filter.Limit > 0 && filter.Limit < len(events) {
		events = events[:filter.Limit]
	}

	return events, scanner.Err()
}

// Close closes the log file
func (l *FileLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *FileLogger) matchesFilter(event *Event, filter Filter) bool {
	if filter.Network != "" && event.Network != filter.Network {
		return false
	}
	if filter.Device != "" && event.Device != filter.Device {
		return false
	}
	if filter.User != "" && event.User != filter.User {
		return false
	}
	if filter.Operation != "" && event.Operation != filter.Operation {
		return false
	}
	if filter.Service != "" && event.Service != filter.Service {
		return false
	}
	if filter.Interface != "" && event.Interface != filter.Interface {
		return false
	}
	if !filter.StartTime.IsZero() && event.Timestamp.Before(filter.StartTime) {
		return false
	}
	if !filter.EndTime.IsZero() && event.Timestamp.After(filter.EndTime) {
		return false
	}
	if filter.SuccessOnly && !event.Success {
		return false
	}
	if filter.FailureOnly && event.Success {
		return false
	}
	return true
}

func (l *FileLogger) rotate() error {
	// Close current file
	if err := l.file.Close(); err != nil {
		return err
	}

	// Rename current file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	rotatedPath := l.path + "." + timestamp

	if err := os.Rename(l.path, rotatedPath); err != nil {
		// Reopen the original file to avoid leaving the logger in a broken state
		if f, reopenErr := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); reopenErr == nil {
			l.file = f
			l.encoder = json.NewEncoder(f)
		}
		return err
	}

	// Open new file
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	l.file = file
	l.encoder = json.NewEncoder(file)

	// Cleanup old files if configured
	if l.rotation.MaxBackups > 0 {
		l.cleanupOldFiles()
	}

	return nil
}

func (l *FileLogger) cleanupOldFiles() {
	dir := filepath.Dir(l.path)
	base := filepath.Base(l.path)
	pattern := base + ".*"

	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return
	}

	// Sort by modification time, newest first
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var files []fileInfo
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path, info.ModTime()})
	}

	// Keep only MaxBackups files
	if len(files) > l.rotation.MaxBackups {
		sort.Slice(files, func(i, j int) bool {
			return files[i].modTime.Before(files[j].modTime)
		})

		// Remove oldest files
		toRemove := len(files) - l.rotation.MaxBackups
		for i := range toRemove {
			if err := os.Remove(files[i].path); err != nil {
				util.Logger.Warnf("audit: failed to remove old log file %s: %v", files[i].path, err)
			}
		}
	}
}

// loggerHolder wraps a Logger so atomic.Value always stores the same concrete type.
type loggerHolder struct {
	logger Logger
}

var defaultLogger atomic.Value

// SetDefaultLogger sets the default audit logger
func SetDefaultLogger(logger Logger) {
	defaultLogger.Store(loggerHolder{logger: logger})
}

func getDefaultLogger() Logger {
	v := defaultLogger.Load()
	if v == nil {
		return nil
	}
	return v.(loggerHolder).logger
}

// Log writes event to the default logger. Silent no-op when no
// default logger is configured (auth-design.md L1 disabled state).
// This is the load-bearing emission path used by the api package's
// audit middleware and by Network.checkPermission decision logging.
func Log(event *Event) error {
	l := getDefaultLogger()
	if l == nil {
		return nil
	}
	return l.Log(event)
}
