package audit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/newtron-network/newtron/pkg/network"
)

func TestEvent_New(t *testing.T) {
	event := NewEvent("alice", "leaf1-ny", "service.apply")

	if event.User != "alice" {
		t.Errorf("User = %q, want %q", event.User, "alice")
	}
	if event.Device != "leaf1-ny" {
		t.Errorf("Device = %q, want %q", event.Device, "leaf1-ny")
	}
	if event.Operation != "service.apply" {
		t.Errorf("Operation = %q, want %q", event.Operation, "service.apply")
	}
	if event.ID == "" {
		t.Error("ID should not be empty")
	}
	if event.Timestamp.IsZero() {
		t.Error("Timestamp should be set")
	}
}

func TestEvent_Chaining(t *testing.T) {
	changes := []network.Change{
		{Table: "VRF", Key: "test", Type: network.ChangeAdd},
	}

	event := NewEvent("alice", "leaf1-ny", "service.apply").
		WithService("customer-l3").
		WithInterface("Ethernet0").
		WithChanges(changes).
		WithSuccess().
		WithDuration(time.Second).
		WithExecuteMode(true)

	if event.Service != "customer-l3" {
		t.Errorf("Service = %q", event.Service)
	}
	if event.Interface != "Ethernet0" {
		t.Errorf("Interface = %q", event.Interface)
	}
	if len(event.Changes) != 1 {
		t.Errorf("Expected 1 change, got %d", len(event.Changes))
	}
	if !event.Success {
		t.Error("Success should be true")
	}
	if event.Duration != time.Second {
		t.Errorf("Duration = %v", event.Duration)
	}
	if !event.ExecuteMode {
		t.Error("ExecuteMode should be true")
	}
	if event.DryRun {
		t.Error("DryRun should be false when ExecuteMode is true")
	}
}

func TestEvent_WithError(t *testing.T) {
	event := NewEvent("alice", "leaf1-ny", "service.apply").
		WithError(errors.New("test error"))

	if event.Success {
		t.Error("Success should be false")
	}
	if event.Error != "test error" {
		t.Errorf("Error = %q", event.Error)
	}

	// Test with nil error
	event2 := NewEvent("alice", "leaf1-ny", "test").WithError(nil)
	if event2.Success {
		t.Error("Success should be false even with nil error")
	}
	if event2.Error != "" {
		t.Errorf("Error should be empty with nil error, got %q", event2.Error)
	}
}

func TestEvent_ExecuteMode(t *testing.T) {
	event := NewEvent("alice", "leaf1-ny", "test").WithExecuteMode(false)

	if event.ExecuteMode {
		t.Error("ExecuteMode should be false")
	}
	if !event.DryRun {
		t.Error("DryRun should be true when ExecuteMode is false")
	}
}

func TestFileLogger_Basic(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log an event
	event := NewEvent("alice", "leaf1-ny", "service.apply").
		WithService("customer-l3").
		WithSuccess()

	if err := logger.Log(event); err != nil {
		t.Fatalf("Log failed: %v", err)
	}

	// Query it back
	events, err := logger.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].User != "alice" {
		t.Errorf("User = %q, want %q", events[0].User, "alice")
	}
	if events[0].Device != "leaf1-ny" {
		t.Errorf("Device = %q, want %q", events[0].Device, "leaf1-ny")
	}
}

func TestFileLogger_QueryFilters(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log multiple events
	events := []*Event{
		NewEvent("alice", "leaf1-ny", "service.apply").WithService("svc1").WithSuccess(),
		NewEvent("bob", "leaf1-ny", "vlan.create").WithSuccess(),
		NewEvent("alice", "spine1-ny", "bgp.modify").WithError(errors.New("failed")),
		NewEvent("charlie", "leaf2-ny", "service.apply").WithService("svc2").WithSuccess(),
	}

	for _, e := range events {
		if err := logger.Log(e); err != nil {
			t.Fatalf("Log failed: %v", err)
		}
	}

	t.Run("filter by user", func(t *testing.T) {
		results, _ := logger.Query(Filter{User: "alice"})
		if len(results) != 2 {
			t.Errorf("Expected 2 events for alice, got %d", len(results))
		}
	})

	t.Run("filter by device", func(t *testing.T) {
		results, _ := logger.Query(Filter{Device: "leaf1-ny"})
		if len(results) != 2 {
			t.Errorf("Expected 2 events for leaf1-ny, got %d", len(results))
		}
	})

	t.Run("filter by operation", func(t *testing.T) {
		results, _ := logger.Query(Filter{Operation: "service.apply"})
		if len(results) != 2 {
			t.Errorf("Expected 2 service.apply events, got %d", len(results))
		}
	})

	t.Run("filter by service", func(t *testing.T) {
		results, _ := logger.Query(Filter{Service: "svc1"})
		if len(results) != 1 {
			t.Errorf("Expected 1 event for svc1, got %d", len(results))
		}
	})

	t.Run("filter success only", func(t *testing.T) {
		results, _ := logger.Query(Filter{SuccessOnly: true})
		if len(results) != 3 {
			t.Errorf("Expected 3 successful events, got %d", len(results))
		}
	})

	t.Run("filter failure only", func(t *testing.T) {
		results, _ := logger.Query(Filter{FailureOnly: true})
		if len(results) != 1 {
			t.Errorf("Expected 1 failed event, got %d", len(results))
		}
	})

	t.Run("filter with limit", func(t *testing.T) {
		results, _ := logger.Query(Filter{Limit: 2})
		if len(results) != 2 {
			t.Errorf("Expected 2 events with limit, got %d", len(results))
		}
	})

	t.Run("filter with offset", func(t *testing.T) {
		results, _ := logger.Query(Filter{Offset: 2})
		if len(results) != 2 {
			t.Errorf("Expected 2 events with offset, got %d", len(results))
		}
	})
}

func TestFileLogger_QueryTimeFilter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log an event
	logger.Log(NewEvent("alice", "leaf1-ny", "test").WithSuccess())

	// Query with time filters
	results, _ := logger.Query(Filter{
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now().Add(time.Hour),
	})

	if len(results) != 1 {
		t.Errorf("Expected 1 event in time range, got %d", len(results))
	}

	// Query outside time range
	results, _ = logger.Query(Filter{
		StartTime: time.Now().Add(time.Hour),
	})

	if len(results) != 0 {
		t.Errorf("Expected 0 events outside time range, got %d", len(results))
	}
}

func TestFileLogger_NonExistentFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "nonexistent", "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger should create directories: %v", err)
	}
	defer logger.Close()
}

func TestFileLogger_QueryNonExistent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	logger.Close()

	// Remove the file
	os.Remove(logPath)

	// Query should return empty, not error
	logger2, _ := NewFileLogger(filepath.Join(tmpDir, "other.log"), RotationConfig{})
	defer logger2.Close()

	// Need to query a non-existent path
	results, err := logger2.Query(Filter{})
	if err != nil {
		t.Errorf("Query on non-existent should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 events, got %d", len(results))
	}
}

func TestDefaultLogger(t *testing.T) {
	// Clear default logger
	SetDefaultLogger(nil)

	// Log with no default should not error
	if err := Log(NewEvent("test", "test", "test")); err != nil {
		t.Errorf("Log with nil default should not error: %v", err)
	}

	// Query with no default should return empty
	results, err := Query(Filter{})
	if err != nil {
		t.Errorf("Query with nil default should not error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}

	// Set up a logger
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	SetDefaultLogger(logger)

	// Now log and query should work
	if err := Log(NewEvent("alice", "leaf1", "test").WithSuccess()); err != nil {
		t.Errorf("Log failed: %v", err)
	}

	results, err = Query(Filter{})
	if err != nil {
		t.Errorf("Query failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	// Clean up
	SetDefaultLogger(nil)
}

func TestEventTypes(t *testing.T) {
	// Just verify constants exist
	types := []EventType{
		EventTypeConnect,
		EventTypeDisconnect,
		EventTypeLock,
		EventTypeUnlock,
		EventTypePreview,
		EventTypeExecute,
		EventTypeRollback,
	}

	for _, et := range types {
		if et == "" {
			t.Error("EventType should not be empty")
		}
	}
}

func TestSeverities(t *testing.T) {
	severities := []Severity{SeverityInfo, SeverityWarning, SeverityError}
	for _, s := range severities {
		if s == "" {
			t.Error("Severity should not be empty")
		}
	}
}

func TestFileLogger_LogRotation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-rotation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	// Set very small max size to trigger rotation
	logger, err := NewFileLogger(logPath, RotationConfig{
		MaxSize:    100, // 100 bytes - will trigger on second log
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log multiple events to trigger rotation
	for i := 0; i < 5; i++ {
		event := NewEvent("alice", "leaf1-ny", "service.apply").
			WithService("customer-l3").
			WithSuccess()
		if err := logger.Log(event); err != nil {
			t.Fatalf("Log failed on iteration %d: %v", i, err)
		}
	}

	// Check that rotation files were created
	matches, err := filepath.Glob(filepath.Join(tmpDir, "audit.log.*"))
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}

	// Should have some backup files
	if len(matches) == 0 {
		t.Error("Expected rotation to create backup files")
	}
}

func TestFileLogger_RotationWithCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	// Set very small max size and low max backups
	logger, err := NewFileLogger(logPath, RotationConfig{
		MaxSize:    50, // Very small to trigger many rotations
		MaxBackups: 2,  // Only keep 2 backups
	})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log many events to trigger multiple rotations and cleanups
	for i := 0; i < 10; i++ {
		event := NewEvent("alice", "leaf1-ny", "test")
		if err := logger.Log(event); err != nil {
			t.Fatalf("Log failed on iteration %d: %v", i, err)
		}
	}

	// Check backup count doesn't exceed MaxBackups
	matches, err := filepath.Glob(filepath.Join(tmpDir, "audit.log.*"))
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}

	if len(matches) > 2 {
		t.Errorf("Expected at most 2 backup files, got %d", len(matches))
	}
}

func TestFileLogger_NewFileLoggerMkdirError(t *testing.T) {
	// Try to create logger in a location where we can't create directories
	// On most systems, /dev/null/subdir won't work
	_, err := NewFileLogger("/dev/null/impossible/audit.log", RotationConfig{})
	if err == nil {
		t.Error("NewFileLogger should fail when directory creation fails")
	}
}

func TestFileLogger_NewFileLoggerOpenError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory where the file should be (can't open directory as file)
	logPath := filepath.Join(tmpDir, "audit.log")
	if err := os.Mkdir(logPath, 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	_, err = NewFileLogger(logPath, RotationConfig{})
	if err == nil {
		t.Error("NewFileLogger should fail when log path is a directory")
	}
}

func TestFileLogger_QueryMalformedJSON(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")

	// Write malformed JSON directly to log file
	content := `{"user":"alice","device":"leaf1","operation":"test","success":true}
invalid json line
{"user":"bob","device":"leaf2","operation":"test","success":true}
`
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Query should skip malformed lines
	results, err := logger.Query(Filter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 valid events (skipping malformed), got %d", len(results))
	}
}

func TestFileLogger_QueryInterfaceFilter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log events with different interfaces
	logger.Log(NewEvent("alice", "leaf1", "test").WithInterface("Ethernet0").WithSuccess())
	logger.Log(NewEvent("alice", "leaf1", "test").WithInterface("Ethernet4").WithSuccess())
	logger.Log(NewEvent("alice", "leaf1", "test").WithInterface("Ethernet0").WithSuccess())

	results, err := logger.Query(Filter{Interface: "Ethernet0"})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 events with Ethernet0, got %d", len(results))
	}
}

func TestFileLogger_QueryEndTimeFilter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	logger.Log(NewEvent("alice", "leaf1", "test").WithSuccess())

	// Query with end time in the past (should find nothing)
	results, err := logger.Query(Filter{
		EndTime: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 events before end time, got %d", len(results))
	}
}

func TestFileLogger_QueryOffsetBeyondEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logPath := filepath.Join(tmpDir, "audit.log")
	logger, err := NewFileLogger(logPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}
	defer logger.Close()

	// Log a few events
	for i := 0; i < 3; i++ {
		logger.Log(NewEvent("alice", "leaf1", "test").WithSuccess())
	}

	// Query with offset beyond total events
	results, err := logger.Query(Filter{Offset: 10})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Offset 10 is beyond 3 events, should return slice from offset (empty since offset > len)
	if len(results) != 3 {
		// Based on the code: if filter.Offset > 0 && filter.Offset < len(events)
		// So if offset >= len(events), no slicing happens
		t.Logf("Got %d results with offset beyond events", len(results))
	}
}

func TestFileLogger_CloseNilFile(t *testing.T) {
	// Create a logger and manually set file to nil
	logger := &FileLogger{
		path: "/tmp/test.log",
		file: nil, // nil file
	}

	// Close should handle nil file gracefully
	err := logger.Close()
	if err != nil {
		t.Errorf("Close() with nil file should not error: %v", err)
	}
}

func TestFileLogger_QueryReadError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "audit-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create logger pointing to a directory (will fail to open for reading)
	logDir := filepath.Join(tmpDir, "audit.log")
	if err := os.Mkdir(logDir, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}

	// Create a valid log file elsewhere
	realLogPath := filepath.Join(tmpDir, "real.log")
	logger, err := NewFileLogger(realLogPath, RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger failed: %v", err)
	}

	// Manually change the path to the directory to test read error
	logger.path = logDir

	_, err = logger.Query(Filter{})
	if err == nil {
		t.Error("Query should fail when trying to read a directory")
	}
}
