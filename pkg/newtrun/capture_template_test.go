package newtrun

import (
	"strings"
	"testing"
)

// TestApplyTemplate_CapturedURL pins the URL-context substitution
// for a captured value. The captured key arrives in the URL path,
// gets PathEscape'd like target/param substitutions do.
func TestApplyTemplate_CapturedURL(t *testing.T) {
	got, err := applyTemplate(
		"/networks/{{captured.net_id}}/nodes",
		nil, nil,
		map[string]any{"net_id": "net 1/2"},
		ctxURL,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "/networks/net%201%2F2/nodes" {
		t.Errorf("got %q, want PathEscape'd net 1/2", got)
	}
}

// TestApplyTemplate_CapturedRawContext pins free-form context (no
// escaping). The session-key use case: a captured key arrives in a
// header value as-is.
func TestApplyTemplate_CapturedRawContext(t *testing.T) {
	got, err := applyTemplate(
		"Bearer {{captured.session_key}}",
		nil, nil,
		map[string]any{"session_key": "abc.123"},
		ctxRaw,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "Bearer abc.123" {
		t.Errorf("got %q", got)
	}
}

// TestApplyTemplate_CapturedUndefined pins the runtime error a
// suite author sees when a {{captured.NAME}} reference has no
// matching entry in the captured map. The error message must name the
// missing key so the author can find the typo or out-of-order
// reference.
func TestApplyTemplate_CapturedUndefined(t *testing.T) {
	_, err := applyTemplate(
		"Bearer {{captured.session_key}}",
		nil, nil,
		map[string]any{},
		ctxRaw,
	)
	if err == nil {
		t.Fatal("err = nil")
	}
	if !strings.Contains(err.Error(), "session_key") {
		t.Errorf("err = %v, want name in message", err)
	}
}

// TestExpandStep_HeadersCaptured pins that the Headers field is
// expanded — without this, a captured session key referenced in
// Authorization would arrive at the server as a literal
// {{captured.NAME}} string.
func TestExpandStep_HeadersCaptured(t *testing.T) {
	step := Step{
		Action: ActionNewtron,
		URL:    "/foo",
		Headers: map[string]string{
			"Authorization":    "Bearer {{captured.key}}",
			"X-Newtron-Caller": "alice",
		},
	}
	expanded, err := ExpandStep(step, nil, nil, map[string]any{"key": "abc"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.Headers["Authorization"] != "Bearer abc" {
		t.Errorf("Authorization = %q, want Bearer abc", expanded.Headers["Authorization"])
	}
	if expanded.Headers["X-Newtron-Caller"] != "alice" {
		t.Errorf("X-Newtron-Caller mangled: %q", expanded.Headers["X-Newtron-Caller"])
	}
}

// TestExpandStep_CapturedTypedThroughParams pins the typed-value
// passthrough for captures. Same contract as targets/params: a
// captured float (the JQ default for JSON numbers) inside a Params
// value that is ENTIRELY one {{captured.X}} token keeps its type
// through json.Marshal — emitted as `3`, not `"3"`.
func TestExpandStep_CapturedTypedThroughParams(t *testing.T) {
	step := Step{
		Action: ActionNewtron,
		URL:    "/foo",
		Params: map[string]any{"count": "{{captured.n}}"},
	}
	expanded, err := ExpandStep(step, nil, nil, map[string]any{"n": float64(3)})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	v, ok := expanded.Params["count"].(float64)
	if !ok || v != 3 {
		t.Errorf("count = %T %v, want float64 3", expanded.Params["count"], expanded.Params["count"])
	}
}

// TestExpandStep_NoCapturedReferences_NoOp pins that a step with no
// captured references survives ExpandStep cleanly even when a
// captured map is passed. The runner relies on this — it always
// passes the map, even on steps that don't reference it.
func TestExpandStep_NoCapturedReferences_NoOp(t *testing.T) {
	step := Step{
		Action:  ActionNewtron,
		URL:     "/foo",
		Headers: map[string]string{"X-Newtron-Caller": "alice"},
	}
	expanded, err := ExpandStep(step, nil, nil, map[string]any{"unused": "x"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.URL != "/foo" {
		t.Errorf("URL changed: %q", expanded.URL)
	}
	if expanded.Headers["X-Newtron-Caller"] != "alice" {
		t.Errorf("Headers changed: %v", expanded.Headers)
	}
}
