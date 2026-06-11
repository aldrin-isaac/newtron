package newtrun

import (
	"strings"
	"testing"
)

// TestParseScenario_CaptureOnBatchRejected pins the parse-time
// guard against capture on batch — batch emits multiple responses
// with no canonical "the response."
func TestParseScenario_CaptureOnBatchRejected(t *testing.T) {
	yaml := `
name: x
steps:
  - name: s
    action: newtron
    batch:
      - method: POST
        url: /a
      - method: POST
        url: /b
    capture:
      x: .data.id
`
	_, err := ParseScenarioBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "capture") || !strings.Contains(err.Error(), "batch") {
		t.Errorf("err = %v, want one mentioning capture+batch", err)
	}
}

// TestParseScenario_CaptureOnPollRejected pins the guard against
// capture on poll. A poll loops; the response it captures from is
// ambiguous (last? first? the one that passed?).
func TestParseScenario_CaptureOnPollRejected(t *testing.T) {
	yaml := `
name: x
steps:
  - name: s
    action: newtron
    method: GET
    url: /status
    poll:
      timeout: 10s
      interval: 1s
    expect:
      jq: .data.ready == true
    capture:
      x: .data.id
`
	_, err := ParseScenarioBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "capture") || !strings.Contains(err.Error(), "poll") {
		t.Errorf("err = %v, want one mentioning capture+poll", err)
	}
}

// TestParseScenario_CaptureOnNonNewtronRejected pins that capture
// is only valid on the newtron action.
func TestParseScenario_CaptureOnNonNewtronRejected(t *testing.T) {
	yaml := `
name: x
steps:
  - name: s
    action: wait
    duration: 1s
    capture:
      x: .data.id
`
	_, err := ParseScenarioBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "capture") || !strings.Contains(err.Error(), "newtron") {
		t.Errorf("err = %v, want one mentioning capture+newtron", err)
	}
}

// TestParseScenario_CaptureEmptyExprRejected pins that an empty JQ
// expression is caught at parse time rather than failing at runtime
// with a confusing "expression produced no output" from gojq.
func TestParseScenario_CaptureEmptyExprRejected(t *testing.T) {
	yaml := `
name: x
steps:
  - name: s
    action: newtron
    method: POST
    url: /foo
    capture:
      x: ""
`
	_, err := ParseScenarioBytes([]byte(yaml))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v, want one mentioning empty", err)
	}
}

// TestParseScenario_CaptureValidShape pins that a well-formed
// capture: parses cleanly.
func TestParseScenario_CaptureValidShape(t *testing.T) {
	yaml := `
name: x
steps:
  - name: s
    action: newtron
    method: POST
    url: /auth/login
    headers:
      Authorization: "Basic YWxpY2U6cHc="
    capture:
      session_key: .key
      user: .user
`
	sc, err := ParseScenarioBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(sc.Steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(sc.Steps))
	}
	if got := sc.Steps[0].Capture["session_key"]; got != ".key" {
		t.Errorf("Capture[session_key] = %q", got)
	}
}
