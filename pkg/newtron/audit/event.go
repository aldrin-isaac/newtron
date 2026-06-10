// Package audit provides audit logging for configuration changes.
package audit

import (
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
	// or test events where no caller was attached. A reviewer
	// reading this in production data is a bug.
	VerificationUnknown VerificationSource = ""

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
)

// Event represents an auditable configuration change event
type Event struct {
	ID                 string             `json:"id"`
	Timestamp          time.Time          `json:"timestamp"`
	User               string             `json:"user"`
	VerificationSource VerificationSource `json:"verification_source,omitempty"`
	Device             string             `json:"device"`
	Operation          string             `json:"operation"`
	Service            string             `json:"service,omitempty"`
	Interface          string             `json:"interface,omitempty"`
	Changes            []node.Change      `json:"changes"`
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
}
