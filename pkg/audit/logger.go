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

	"github.com/newtron-network/newtron/pkg/util"
)

// Logger defines the interface for audit logging backends
type Logger interface {
	Log(event *Event) error
	Query(filter Filter) ([]*Event, error)
	Close() error
}

// FileLogger logs audit events to a JSON-lines file
type FileLogger struct {
	path     string
	file     *os.File
	encoder  *json.Encoder
	mu       sync.RWMutex
	rotation RotationConfig
}

// RotationConfig configures log file rotation
type RotationConfig struct {
	MaxSize    int64 // Max file size in bytes before rotation
	MaxBackups int   // Max number of old files to retain
}

// NewFileLogger creates a new file-based audit logger
func NewFileLogger(path string, rotation RotationConfig) (*FileLogger, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}

	return &FileLogger{
		path:     path,
		file:     file,
		encoder:  json.NewEncoder(file),
		rotation: rotation,
	}, nil
}

// Log writes an audit event to the log file
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

	return l.encoder.Encode(event)
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
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			util.Warnf("audit: skipping malformed log entry at line %d: %v", lineNum, err)
			continue
		}

		if l.matchesFilter(&event, filter) {
			events = append(events, &event)
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
		for i := 0; i < toRemove; i++ {
			os.Remove(files[i].path)
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

// Log logs an event using the default logger
func Log(event *Event) error {
	l := getDefaultLogger()
	if l == nil {
		return nil // No-op if no logger configured
	}
	return l.Log(event)
}

// Query queries events from the default logger
func Query(filter Filter) ([]*Event, error) {
	l := getDefaultLogger()
	if l == nil {
		return []*Event{}, nil
	}
	return l.Query(filter)
}
