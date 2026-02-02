//go:build e2e

package e2e_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/audit"
	"github.com/newtron-network/newtron/pkg/operations"
)

func TestE2E_AuditLogCreation(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Audit", "none")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	logger, err := audit.NewFileLogger(logPath, audit.RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer logger.Close()

	event := audit.NewEvent("testuser", "leaf1", string(audit.EventTypeExecute)).WithSuccess()
	if err := logger.Log(event); err != nil {
		t.Fatalf("Log: %v", err)
	}

	events, err := logger.Query(audit.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.User != "testuser" {
		t.Errorf("User = %q, want %q", e.User, "testuser")
	}
	if e.Device != "leaf1" {
		t.Errorf("Device = %q, want %q", e.Device, "leaf1")
	}
	if e.Operation != string(audit.EventTypeExecute) {
		t.Errorf("Operation = %q, want %q", e.Operation, audit.EventTypeExecute)
	}
	if !e.Success {
		t.Error("expected Success = true")
	}
}

func TestE2E_AuditQueryFilter(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Audit", "none")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	logger, err := audit.NewFileLogger(logPath, audit.RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer logger.Close()

	// Log 3 events: 2 on leaf1 (1 success, 1 failure), 1 on leaf2 (success)
	e1 := audit.NewEvent("user1", "leaf1", string(audit.EventTypeExecute)).WithSuccess()
	e2 := audit.NewEvent("user1", "leaf1", string(audit.EventTypeExecute)).WithError(nil)
	// WithError(nil) sets Success=false but Error=""
	e2.Success = false
	e3 := audit.NewEvent("user2", "leaf2", string(audit.EventTypePreview)).WithSuccess()

	// Small sleep between events so timestamps differ
	if err := logger.Log(e1); err != nil {
		t.Fatalf("Log e1: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := logger.Log(e2); err != nil {
		t.Fatalf("Log e2: %v", err)
	}
	time.Sleep(time.Millisecond)
	if err := logger.Log(e3); err != nil {
		t.Fatalf("Log e3: %v", err)
	}

	// Filter by device
	events, err := logger.Query(audit.Filter{Device: "leaf1"})
	if err != nil {
		t.Fatalf("Query device filter: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("device filter: expected 2 events, got %d", len(events))
	}

	// Filter by success only
	events, err = logger.Query(audit.Filter{SuccessOnly: true})
	if err != nil {
		t.Fatalf("Query success filter: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("success filter: expected 2 events, got %d", len(events))
	}

	// Filter with limit
	events, err = logger.Query(audit.Filter{Limit: 1})
	if err != nil {
		t.Fatalf("Query limit filter: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("limit filter: expected 1 event, got %d", len(events))
	}
}

func TestE2E_AuditEventTypes(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Audit", "none")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	logger, err := audit.NewFileLogger(logPath, audit.RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer logger.Close()

	eventTypes := []audit.EventType{
		audit.EventTypeConnect,
		audit.EventTypeDisconnect,
		audit.EventTypeLock,
		audit.EventTypeUnlock,
		audit.EventTypePreview,
		audit.EventTypeExecute,
		audit.EventTypeRollback,
	}

	for _, et := range eventTypes {
		event := audit.NewEvent("testuser", "leaf1", string(et)).WithSuccess()
		if err := logger.Log(event); err != nil {
			t.Fatalf("Log %s: %v", et, err)
		}
	}

	// Query each type and verify exactly 1 result
	for _, et := range eventTypes {
		t.Run(string(et), func(t *testing.T) {
			events, err := logger.Query(audit.Filter{Operation: string(et)})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(events) != 1 {
				t.Errorf("expected 1 event for type %q, got %d", et, len(events))
			}
		})
	}
}

func TestE2E_AuditOperationGeneratesEvent(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Audit", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Set up FileLogger as default
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.jsonl")

	logger, err := audit.NewFileLogger(logPath, audit.RotationConfig{})
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer logger.Close()

	oldLogger := audit.DefaultLogger
	audit.SetDefaultLogger(logger)
	t.Cleanup(func() {
		audit.SetDefaultLogger(oldLogger)
	})

	// Cleanup: delete VLAN 520 via Redis
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		client.Del(context.Background(), "VLAN|Vlan520")
	})

	// Perform a VLAN create
	dev := testutil.LabLockedDevice(t, nodeName)
	op := &operations.CreateVLANOp{ID: 520, Desc: "e2e-audit-test"}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Manually log an audit event for the operation
	event := audit.NewEvent("e2e-test", nodeName, string(audit.EventTypeExecute)).
		WithService("vlan-create").
		WithSuccess().
		WithDuration(time.Millisecond * 100)
	if err := audit.Log(event); err != nil {
		t.Fatalf("audit.Log: %v", err)
	}

	// Query for events
	events, err := audit.Query(audit.Filter{Device: nodeName})
	if err != nil {
		t.Fatalf("audit.Query: %v", err)
	}

	if len(events) == 0 {
		t.Fatal("expected at least 1 audit event")
	}

	found := false
	for _, e := range events {
		if e.Service == "vlan-create" && e.Device == nodeName {
			found = true
			break
		}
	}
	if !found {
		t.Error("audit event for VLAN create not found")
	}
}
