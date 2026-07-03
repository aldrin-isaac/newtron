package api

import (
	"encoding/json"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// TestExtractError pins that the underlying failure reason is pulled out of the
// APIResponse envelope's `error` field (the same string the caller saw live),
// and that bodies without a usable message yield "" so the middleware falls back
// to the HTTP status text.
func TestExtractError(t *testing.T) {
	body, err := json.Marshal(httputil.APIResponse{Error: "l3vni must be an integer in 1..16777215"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := extractError(body); got != "l3vni must be an integer in 1..16777215" {
		t.Errorf("extractError = %q; want the underlying reason", got)
	}

	// A structured conflict envelope still surfaces its `error` message.
	conflict := []byte(`{"error":"IPVPNSpec 'IRB' has 2 references: ...","data":{"resource":"IPVPNSpec","references":["..."],"force_available":false}}`)
	if got := extractError(conflict); got == "" {
		t.Errorf("extractError on a conflict envelope = %q; want the message", got)
	}

	// Bodies with no usable message → "" (caller falls back to status text).
	for name, in := range map[string][]byte{
		"empty":        nil,
		"no-error-key": []byte(`{"data":{"name":"transit"}}`),
		"not-json":     []byte(`<html>500</html>`),
	} {
		if got := extractError(in); got != "" {
			t.Errorf("%s: extractError = %q; want empty", name, got)
		}
	}
}

// TestExtractChanges pins that the change-set is pulled out of the standard
// APIResponse envelope a device write returns, and that shapes without a
// change-set (spec-authoring results, errors, garbage) yield nil rather than
// failing.
func TestExtractChanges(t *testing.T) {
	// A faithful device-write response: WriteResult inside the envelope.
	wr := newtron.WriteResult{
		ChangeCount: 1,
		Applied:     true,
		Changes: []sonic.ConfigChange{
			{Table: "VLAN", Key: "Vlan100", Type: sonic.ChangeTypeAdd, Fields: map[string]string{"vlanid": "100"}},
		},
	}
	body, err := json.Marshal(httputil.APIResponse{Data: wr})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := extractChanges(body)
	if len(got) != 1 {
		t.Fatalf("extractChanges got %d changes; want 1", len(got))
	}
	if got[0].Table != "VLAN" || got[0].Key != "Vlan100" {
		t.Errorf("extractChanges returned %+v; want VLAN/Vlan100", got[0])
	}

	// Shapes that must degrade to nil, never error.
	for name, in := range map[string][]byte{
		"empty":              nil,
		"spec-op-no-changes": []byte(`{"data":{"name":"transit","created":true}}`),
		"error-envelope":     []byte(`{"error":"boom"}`),
		"not-json":           []byte(`<html>500</html>`),
	} {
		if got := extractChanges(in); got != nil {
			t.Errorf("%s: extractChanges = %+v; want nil", name, got)
		}
	}
}

// TestRedactRequestBody pins the secret-handling contract: plaintext values
// under sensitive keys are masked, ${secret:…} references are preserved (they
// are pointers, not secrets), nesting is handled at any depth, and an
// unparseable body is never stored verbatim.
func TestRedactRequestBody(t *testing.T) {
	in := []byte(`{
		"username": "alice",
		"ssh_pass": "hunter2",
		"nodeSpec": {"password": "nested-secret", "token": "${secret:API_TOKEN}"},
		"peers": [{"secret": "leaf-psk"}]
	}`)

	out := redactRequestBody(in, "/newtron/v1/networks/n/create-service")
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("redacted body did not round-trip: %v", err)
	}

	if got["username"] != "alice" {
		t.Errorf("username = %v; want preserved 'alice'", got["username"])
	}
	if got["ssh_pass"] != redactedPlaceholder {
		t.Errorf("ssh_pass = %v; want redacted", got["ssh_pass"])
	}
	nodeSpec := got["nodeSpec"].(map[string]any)
	if nodeSpec["password"] != redactedPlaceholder {
		t.Errorf("nested password = %v; want redacted", nodeSpec["password"])
	}
	if nodeSpec["token"] != "${secret:API_TOKEN}" {
		t.Errorf("token = %v; want preserved ${secret:…} reference", nodeSpec["token"])
	}
	peer := got["peers"].([]any)[0].(map[string]any)
	if peer["secret"] != redactedPlaceholder {
		t.Errorf("secret-in-array = %v; want redacted", peer["secret"])
	}
}

// TestRedactRequestBody_PathScoped pins that a generically-named field is
// redacted ONLY on the endpoint that carries a secret there: `value` is masked
// on POST .../secrets but left intact on an unrelated endpoint (so a non-secret
// `value` field elsewhere is not silently redacted).
func TestRedactRequestBody_PathScoped(t *testing.T) {
	body := []byte(`{"key": "switch1_ssh_pass", "value": "hunter2"}`)

	// On the secrets endpoint, `value` is the credential → redacted; the key,
	// which is not a secret, is preserved so an auditor sees which key was set.
	onSecrets := decodeMap(t, redactRequestBody(body, "/newtron/v1/networks/n/secrets"))
	if onSecrets["value"] != redactedPlaceholder {
		t.Errorf("value on /secrets = %v; want redacted", onSecrets["value"])
	}
	if onSecrets["key"] != "switch1_ssh_pass" {
		t.Errorf("key on /secrets = %v; want preserved", onSecrets["key"])
	}

	// On any other endpoint, a bare `value` is NOT a global secret → preserved.
	elsewhere := decodeMap(t, redactRequestBody([]byte(`{"property": "mtu", "value": "9000"}`),
		"/newtron/v1/networks/n/nodes/d/interfaces/e/set-property"))
	if elsewhere["value"] != "9000" {
		t.Errorf("value on set-property = %v; want preserved (not globally redacted)", elsewhere["value"])
	}
}

func decodeMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode redacted body: %v", err)
	}
	return m
}

// TestRedactRequestBody_EdgeCases pins the no-body and unparseable paths.
func TestRedactRequestBody_EdgeCases(t *testing.T) {
	if got := redactRequestBody(nil, ""); got != nil {
		t.Errorf("nil body: got %s; want nil", got)
	}
	if got := redactRequestBody([]byte("   "), ""); got != nil {
		t.Errorf("whitespace body: got %s; want nil", got)
	}
	// Unparseable: must not be stored verbatim — a body we can't inspect for
	// secrets must not leak into the log.
	got := redactRequestBody([]byte(`{not valid json`), "")
	if string(got) == `{not valid json` {
		t.Errorf("unparseable body stored verbatim; want placeholder")
	}
	if len(got) == 0 {
		t.Errorf("unparseable body dropped silently; want a 'not recorded' marker")
	}
}
