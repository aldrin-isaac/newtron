// Package audit provides audit logging for configuration changes.
package audit

import (
	"encoding/json"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
)

// VerificationSource names how the User field in an Event was
// verified by the server (auth-design.md L1). The audit log
// records this alongside User so a reviewer can distinguish
// entries based on verified identity from entries based on a
// self-attested header.
type VerificationSource string

const (
	// VerificationUnknown is the zero value. Set on synthetic
	// or test events where the audit middleware never ran, so no
	// caller context was ever attached. A reviewer reading this in
	// production data is a bug — a real request that simply carried
	// no identity is VerificationAnonymous, not this.
	VerificationUnknown VerificationSource = ""

	// VerificationAnonymous means a real request reached the handler
	// carrying no caller identity, and the server accepted it — it was
	// operating in permissive mode (no identity source required for
	// this request). This is an honest, expected record on a server
	// with no auth layer wired, distinct from VerificationUnknown
	// (which is a bug). Transports that require identity (mTLS with a
	// client CA, PAM) reject before the handler, so reaching audit
	// emission with no caller means the request was genuinely
	// anonymous-by-policy. The User field is empty.
	VerificationAnonymous VerificationSource = "anonymous"

	// VerificationUnixPeerCreds means the User came from
	// SO_PEERCRED on a Unix-domain socket listener — the kernel
	// attests the connecting process's UID; getpwuid resolves
	// it to a username. Verified (auth-design.md L1).
	VerificationUnixPeerCreds VerificationSource = "unix_peer_creds"

	// VerificationSelfAttestedHeader means the User came from an
	// HTTP header (default X-Newtron-Caller) on a TCP listener
	// and was not verified by the server. Audit-only; a reviewer
	// must treat the User as a self-claim, not a proven identity.
	// Promoted out of L1 when L2b (PAM) lands.
	VerificationSelfAttestedHeader VerificationSource = "self_attested_header"

	// VerificationPAM means the User came from a successful
	// pam_authenticate against the host's PAM stack (L2b). Verified.
	VerificationPAM VerificationSource = "pam"

	// VerificationServiceCertCN means the User came from the CN
	// of a verified X.509 client certificate on an mTLS
	// connection (L2a). Verified.
	VerificationServiceCertCN VerificationSource = "service_cert_cn"

	// VerificationSessionKey means the User came from a
	// server-issued opaque session key (L2c). The original
	// authentication was PAM at /auth/login; this verification
	// source means "a previously-issued key resolved to this
	// username in the in-memory session-key store and the key
	// has not expired or been revoked." Verified — equivalent
	// strength to VerificationPAM within the key's TTL.
	VerificationSessionKey VerificationSource = "session_key"
)

// Event represents an auditable configuration change event.
//
// ID is empty by default. With audit-design.md L6 hash-chain
// integrity enabled, ID is populated by FileLogger.Log with
// SHA256(prev_hash || canonical_json_of_event_with_empty_id).
// PrevHash carries the previous entry's ID so a verifier can walk
// the chain and detect any tampered entry — the first broken hash
// reveals the position of the alteration.
type Event struct {
	ID                 string             `json:"id"`
	PrevHash           string             `json:"prev_hash,omitempty"`
	Timestamp          time.Time          `json:"timestamp"`
	User               string             `json:"user"`
	VerificationSource VerificationSource `json:"verification_source,omitempty"`
	// Network is the network the event was scoped to — the {netID} of
	// the request path, or the network id an authorization decision was
	// evaluated against. It is the scope dimension the per-network audit
	// read path filters on, so an operator authorized to read one
	// network's audit sees only that network's events. Empty for events
	// with no network context (e.g. network creation, a server-registry
	// lifecycle act rather than a network-scoped mutation).
	Network            string             `json:"network,omitempty"`
	Device             string             `json:"device"`
	Operation          string             `json:"operation"`
	Service            string             `json:"service,omitempty"`
	Interface          string             `json:"interface,omitempty"`
	// Resource and Field are populated on authcheck:* decision events
	// (auth-design.md L3+L5). Resource is the specific entity acted on
	// (vlan id, vrf name, …); Field is the meta-authorization dimension
	// — the top-level spec area being mutated (services, permissions,
	// user_groups, …). Reviewers reconstruct the full L5 where-clause
	// evaluation context from these plus Device/Service/Interface.
	Resource string `json:"resource,omitempty"`
	Field    string `json:"field,omitempty"`
	Changes            []node.Change      `json:"changes"`
	// RequestBody is the raw JSON payload the caller submitted, captured by
	// the audit middleware with secret-bearing fields redacted. It answers
	// "what did this operation submit?" — the content half of an audit trail,
	// distinct from Changes (what the operation produced on the device). Empty
	// for requests with no body and for read/no-op operations. Part of the
	// hash-chained record: it is content the audit trail must preserve, so it
	// is hashed alongside the rest of the event (audit-design.md L6). Served
	// only by the per-event detail endpoint, never the paged list — bodies are
	// unbounded and the list stays lean.
	RequestBody json.RawMessage `json:"request_body,omitempty"`
	Success            bool               `json:"success"`
	Error              string             `json:"error,omitempty"`
	ExecuteMode        bool               `json:"execute_mode"` // true if -x was used
	DryRun             bool               `json:"dry_run"`
	Duration           time.Duration      `json:"duration"`
	ClientIP           string             `json:"client_ip,omitempty"`
	SessionID          string             `json:"session_id,omitempty"`
}

// Filter defines criteria for querying audit events
type Filter struct {
	// Network scopes results to one network's events (matched against
	// Event.Network). Empty matches every network.
	Network     string
	Device      string
	User        string
	Operation   string
	Service     string
	Interface   string
	StartTime   time.Time
	EndTime     time.Time
	SuccessOnly bool
	FailureOnly bool
	Limit       int
	Offset      int
	// Order selects result ordering, applied before Offset/Limit so paging
	// starts from the chosen end. Empty or OrderNewestFirst returns the
	// most recent events first (the default — an audit log is read to see
	// what just happened, so offset 0 should be recent activity, not the
	// oldest record ever written). OrderOldestFirst returns chronological
	// (hash-chain build) order.
	Order string
}

// Audit result ordering for Filter.Order — the wire values for the HTTP
// `order` query parameter and the CLI `--order` flag.
const (
	OrderNewestFirst = "desc" // default
	OrderOldestFirst = "asc"
)
