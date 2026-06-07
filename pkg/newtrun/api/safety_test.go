package api

import (
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

func mustParse(t *testing.T, yaml string) *newtrun.Scenario {
	t.Helper()
	s, err := newtrun.ParseScenarioBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return s
}

func TestSafetyDefaultsAcceptWaitOnly(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: pause
    action: wait
    duration: 10ms
`)
	if err := p.Validate(s); err != nil {
		t.Errorf("expected accept, got: %v", err)
	}
}

func TestSafetyDefaultsAcceptNewtronAction(t *testing.T) {
	// With no URL allow-list set, the newtron action is accepted at any URL.
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: call
    action: newtron
    method: GET
    url: /api/something
`)
	if err := p.Validate(s); err != nil {
		t.Errorf("expected accept, got: %v", err)
	}
}

func TestSafetyRejectsRequires(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
requires: [other-scenario]
steps:
  - name: pause
    action: wait
    duration: 10ms
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !strings.Contains(err.Error(), "requires") {
		t.Errorf("error should mention requires: %v", err)
	}
}

func TestSafetyRejectsAfter(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
after: [other-scenario]
steps:
  - name: pause
    action: wait
    duration: 10ms
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !strings.Contains(err.Error(), "after") {
		t.Errorf("error should mention after: %v", err)
	}
}

func TestSafetyRejectsHostExec(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: shell
    action: host-exec
    devices: [host-a]
    command: echo
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !strings.Contains(err.Error(), "host-exec") {
		t.Errorf("error should mention host-exec: %v", err)
	}
}

func TestSafetyRejectsNewtronCLI(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: cli
    action: newtron-cli
    devices: [host-a]
    command: show version
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !strings.Contains(err.Error(), "newtron-cli") {
		t.Errorf("error should mention newtron-cli: %v", err)
	}
}

func TestSafetyRejectsReconcileWithoutOptIn(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: reconcile
    action: topology-reconcile
    devices: all
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !strings.Contains(err.Error(), "topology-reconcile") {
		t.Errorf("error should mention topology-reconcile: %v", err)
	}
}

func TestSafetyAcceptsReconcileWhenOptedIn(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	p.AllowReconcile = true
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: reconcile
    action: topology-reconcile
    devices: all
`)
	if err := p.Validate(s); err != nil {
		t.Errorf("expected accept, got: %v", err)
	}
}

func TestSafetyURLPrefixEnforced(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	p.AllowedURLPrefixes = []string{"/api/networks/"}
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: call
    action: newtron
    method: GET
    url: /api/intent/projection
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection due to URL prefix, got nil")
	}
	if !strings.Contains(err.Error(), "prefix") {
		t.Errorf("error should mention prefix: %v", err)
	}
}

func TestSafetyURLPrefixAccepted(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	p.AllowedURLPrefixes = []string{"/api/networks/"}
	s := mustParse(t, `
name: ok
topology: t
steps:
  - name: call
    action: newtron
    method: POST
    url: /api/networks/default/nodes/sw1/something
`)
	if err := p.Validate(s); err != nil {
		t.Errorf("expected accept, got: %v", err)
	}
}

func TestSafetyAccumulatesMultipleReasons(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	s := mustParse(t, `
name: ok
topology: t
requires: [other]
steps:
  - name: shell
    action: host-exec
    devices: [h1]
    command: ls
  - name: cli
    action: newtron-cli
    devices: [h1]
    command: show
`)
	err := p.Validate(s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	var violation *SafetyViolation
	if !asViolation(err, &violation) {
		t.Fatalf("expected *SafetyViolation, got %T", err)
	}
	if len(violation.Reasons) < 3 {
		t.Errorf("expected at least 3 reasons (requires + 2 banned actions), got %d: %v",
			len(violation.Reasons), violation.Reasons)
	}
}

func TestSafetyWallTimeBudgetDefault(t *testing.T) {
	p := DefaultInlineSafetyPolicy()
	if p.WallTimeBudget != 60*time.Second {
		t.Errorf("WallTimeBudget default: got %v, want 60s", p.WallTimeBudget)
	}
}

func asViolation(err error, out **SafetyViolation) bool {
	if v, ok := err.(*SafetyViolation); ok {
		*out = v
		return true
	}
	return false
}
