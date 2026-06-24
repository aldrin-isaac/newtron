// audit_capture.go — content extraction for the audit middleware.
//
// auditMiddleware (audit_middleware.go) records who/when/verb/outcome for
// every mutation. These helpers add the *content*: what the caller submitted
// (redactRequestBody), what the operation produced on the device
// (extractChanges), and — on failure — why it failed (extractError). All
// operate on raw HTTP bytes the middleware captured, so they cover every
// mutation handler uniformly — network-level spec authoring (create-service,
// create-zone) and device writes (apply-service, create-vlan) alike — without
// any handler needing to participate.
package api

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
)

// extractChanges pulls the CONFIG_DB / intent change-set out of a captured
// response body. Device writes return a WriteResult whose `changes` field
// carries the rows the operation added/removed/updated; that result is the
// `data` member of the standard APIResponse envelope. Spec-authoring
// operations return other shapes with no `changes` field — those yield nil,
// which is correct: their effect is the submitted body, not a device change.
//
// Parsing the response (rather than threading the typed result out of every
// handler) keeps capture uniform at the one HTTP layer that already audits
// every mutation. A body that doesn't parse, or carries no changes, yields nil
// — the audit record degrades to the envelope, never fails.
func extractChanges(respBody []byte) []node.Change {
	if len(respBody) == 0 {
		return nil
	}
	var envelope struct {
		Data struct {
			Changes []sonic.ConfigChange `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil
	}
	if len(envelope.Data.Changes) == 0 {
		return nil
	}
	return envelope.Data.Changes
}

// extractError pulls the underlying failure reason out of a captured error
// response body — the `error` member of the standard APIResponse envelope, the
// same string the caller received live (e.g. "l3vni must be an integer in
// 1..16777215", or a referential-conflict message). The audit middleware
// records this on a failed event so the trail is as actionable after the fact
// as it was at request time, instead of the bare HTTP status text. Returns ""
// when the body is absent, doesn't parse (e.g. truncated past the capture cap,
// or a non-JSON error page), or carries no message — the caller then falls back
// to the status text so the event is never left without a failure hint.
func extractError(respBody []byte) string {
	if len(respBody) == 0 {
		return ""
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return ""
	}
	return envelope.Error
}

// redactSensitiveKeys names the request-body fields whose values are secrets in
// the clear and must never reach the audit log verbatim. A ${secret:KEY}
// reference in one of these fields is a pointer, not a secret, so it is left
// intact — that is the whole point of the secret-store indirection (#176/#180),
// and an auditor needs to see which key was referenced.
var redactSensitiveKeys = map[string]bool{
	"ssh_pass":    true,
	"password":    true,
	"passwd":      true,
	"secret":      true,
	"token":       true,
	"private_key": true,
}

// redactedPlaceholder replaces a redacted secret value in the recorded body.
const redactedPlaceholder = "***redacted***"

// redactRequestBody returns the captured request payload with secret-bearing
// fields masked, ready to store on the audit event. The body is parsed as JSON
// and walked recursively; any value under a redactSensitiveKeys field is
// replaced unless it is a ${secret:…} reference. A body that isn't a JSON
// object (or doesn't parse) is dropped rather than stored raw — a payload we
// can't inspect for secrets must not land in the audit log verbatim.
func redactRequestBody(reqBody []byte) json.RawMessage {
	if len(bytes.TrimSpace(reqBody)) == 0 {
		return nil
	}
	var parsed any
	if err := json.Unmarshal(reqBody, &parsed); err != nil {
		// Unparseable (or truncated past the cap): we can't guarantee it
		// holds no secret, so record that a body existed without its content.
		return json.RawMessage(`"<unparseable request body, not recorded>"`)
	}
	redactValue(parsed)
	out, err := json.Marshal(parsed)
	if err != nil {
		return nil
	}
	return out
}

// redactValue walks a decoded JSON value in place, masking the value of any
// sensitive key (redactScalar) and recursing through objects and arrays so a
// nested sensitive field is caught at any depth.
func redactValue(v any) {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			if redactSensitiveKeys[strings.ToLower(k)] {
				val[k] = redactScalar(child)
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range val {
			redactValue(child)
		}
	}
}

// redactScalar masks a value found under a sensitive key. A ${secret:…}
// reference string is preserved (it is a pointer, not the secret — see
// secret.IsRef). Any other string is masked. Non-string values (numbers,
// bools, objects) under a sensitive key are masked wholesale to the
// placeholder — a structured secret is still a secret.
func redactScalar(v any) any {
	if s, ok := v.(string); ok && secret.IsRef(s) {
		return s
	}
	return redactedPlaceholder
}
