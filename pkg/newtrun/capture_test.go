package newtrun

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRunJQ_TopLevelField pins the common case: a
// flat field path against a typical newtron envelope.
func TestRunJQ_TopLevelField(t *testing.T) {
	raw := json.RawMessage(`{"data":{"id":"net-42","status":"ok"},"error":""}`)
	got, err := runJQ(".data.id", raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "net-42" {
		t.Errorf("got %v, want net-42", got)
	}
}

// TestRunJQ_TypedValue pins that JQ preserves the
// JSON value's Go type. A captured int parameter must travel
// through subsequent template substitution as an int, not a string,
// so JSON marshal emits the integer literal.
func TestRunJQ_TypedValue(t *testing.T) {
	raw := json.RawMessage(`{"data":{"count":42,"enabled":true}}`)
	count, err := runJQ(".data.count", raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// gojq decodes JSON numbers as float64 (the same default Go
	// json.Unmarshal uses when decoding into interface{}).
	if v, ok := count.(float64); !ok || v != 42 {
		t.Errorf("count = %T %v, want float64 42", count, count)
	}
	enabled, err := runJQ(".data.enabled", raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v, ok := enabled.(bool); !ok || v != true {
		t.Errorf("enabled = %T %v, want bool true", enabled, enabled)
	}
}

// TestRunJQ_MissingPath pins JQ's nil-on-missing
// semantics. A path that doesn't exist returns nil without error —
// the {{captured.NAME}} expansion later surfaces this as an
// undefined-reference error rather than silently embedding "<nil>".
func TestRunJQ_MissingPath(t *testing.T) {
	raw := json.RawMessage(`{"data":{"id":"x"}}`)
	got, err := runJQ(".data.does_not_exist", raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for missing path", got)
	}
}

// TestRunJQ_BadJSON pins that a body the server
// shouldn't have sent (non-JSON) surfaces as a decode error, not a
// nil-captured-silently.
func TestRunJQ_BadJSON(t *testing.T) {
	raw := json.RawMessage(`not json`)
	_, err := runJQ(".data", raw)
	if err == nil {
		t.Fatal("err = nil, want non-nil for bad JSON")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want one mentioning decode", err)
	}
}

// TestRunJQ_BadExpr pins that a malformed JQ expr
// fails at extract time with a parse error message.
func TestRunJQ_BadExpr(t *testing.T) {
	raw := json.RawMessage(`{"a":1}`)
	_, err := runJQ(".[unclosed", raw)
	if err == nil {
		t.Fatal("err = nil, want non-nil for bad expr")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want one mentioning parse", err)
	}
}

// TestApplyCaptures_MultipleEntries pins that every entry in the
// capture map is applied. The captured must hold all three after a
// successful call; partial-write semantics on error are tested
// separately.
func TestApplyCaptures_MultipleEntries(t *testing.T) {
	raw := json.RawMessage(`{"data":{"id":"net-1","user":"alice","count":3}}`)
	captured := map[string]any{}
	captures := map[string]string{
		"net_id": ".data.id",
		"user":   ".data.user",
		"count":  ".data.count",
	}
	if err := applyCaptures(captured, captures, raw); err != nil {
		t.Fatalf("err = %v", err)
	}
	if captured["net_id"] != "net-1" {
		t.Errorf("net_id = %v", captured["net_id"])
	}
	if captured["user"] != "alice" {
		t.Errorf("user = %v", captured["user"])
	}
	if v, ok := captured["count"].(float64); !ok || v != 3 {
		t.Errorf("count = %T %v", captured["count"], captured["count"])
	}
}

// TestApplyCaptures_NameInErrorMessage pins that a JQ failure in
// one entry names the offending variable, so a suite author whose
// capture map has many entries can tell which one broke.
func TestApplyCaptures_NameInErrorMessage(t *testing.T) {
	raw := json.RawMessage(`{"a":1}`)
	captured := map[string]any{}
	captures := map[string]string{"bad_one": ".[unclosed"}
	err := applyCaptures(captured, captures, raw)
	if err == nil {
		t.Fatal("err = nil")
	}
	if !strings.Contains(err.Error(), "bad_one") {
		t.Errorf("err = %v, want it to name bad_one", err)
	}
}

// TestStepReferencesCaptured_URL pins detection of {{captured.X}}
// in the URL field, the common case driving the non-parameterized
// expansion path.
func TestStepReferencesCaptured_URL(t *testing.T) {
	step := Step{URL: "/foo/{{captured.session_key}}"}
	if !stepReferencesCaptured(step) {
		t.Error("URL reference not detected")
	}
}

// TestStepReferencesCaptured_Headers pins the auth use case: a
// session key captured at /auth/login arrives in subsequent steps'
// Authorization headers.
func TestStepReferencesCaptured_Headers(t *testing.T) {
	step := Step{Headers: map[string]string{"Authorization": "Bearer {{captured.session_key}}"}}
	if !stepReferencesCaptured(step) {
		t.Error("Headers reference not detected")
	}
}

// TestStepReferencesCaptured_NestedParams pins detection inside a
// nested params map — a capture might be referenced in a body
// field, not just URL/headers.
func TestStepReferencesCaptured_NestedParams(t *testing.T) {
	step := Step{Params: map[string]any{
		"outer": map[string]any{"inner": "{{captured.x}}"},
	}}
	if !stepReferencesCaptured(step) {
		t.Error("nested params reference not detected")
	}
}

// TestStepReferencesCaptured_None pins that a step with zero
// captured tokens reports false. Used by the runner to skip the
// ExpandStep call on non-parameterized scenarios that don't use
// captures.
func TestStepReferencesCaptured_None(t *testing.T) {
	step := Step{
		URL:     "/foo/bar",
		Headers: map[string]string{"X-Newtron-Caller": "alice"},
		Params:  map[string]any{"name": "x"},
	}
	if stepReferencesCaptured(step) {
		t.Error("false positive on zero-token step")
	}
}
