//go:build linux

// Package pamauth provides the libpam-backed Authenticator
// implementation that satisfies httputil.Authenticator for the L2b
// user-to-service path (auth-design.md L2b).
//
// This package is split from pkg/httputil because it depends on cgo
// + libpam0g-dev at build time. The interface and middleware in
// pkg/httputil remain cgo-free; only deployments that wire a
// PAMAuthenticator pull in the cgo binding. Operators who don't run
// PAM can omit this package's import from their cmd build without
// touching the middleware contract.
package pamauth

import (
	"fmt"

	"github.com/msteinert/pam/v2"
)

// PAMAuthenticator authenticates username+password against the
// host's PAM stack for the configured service name (auth-design.md
// L2b). Linux-only — the project's stated platform target.
//
// ServiceName is the name of the PAM service config under
// /etc/pam.d/. Typical values: "newtron-server", "newtlab-server",
// "newtrun-server"; operators may share one service config across
// the three engines or configure them differently (e.g., to gate
// newtrun-server behind a stricter group requirement).
//
// Each Authenticate call performs a fresh pam_start →
// pam_authenticate → pam_acct_mgmt → pam_end cycle. PAM transactions
// are not cached because PAM modules may rate-limit, log, or
// otherwise change behavior between attempts — letting libpam see
// every attempt directly is what its modules expect.
type PAMAuthenticator struct {
	ServiceName string
}

// Authenticate calls into libpam to verify username+password
// against the configured PAM service. Returns nil on success; the
// underlying PAM error otherwise (callers map this to HTTP 401).
//
// The PAM conversation callback supplies password to any
// echo-off / echo-on prompt the PAM stack issues. Info and error
// messages from the stack are silently swallowed — surfacing them
// to the HTTP client would leak module-internal detail that
// belongs in the server log, not the response.
func (a *PAMAuthenticator) Authenticate(username, password string) error {
	if a.ServiceName == "" {
		return fmt.Errorf("PAMAuthenticator: ServiceName is empty")
	}
	tx, err := pam.StartFunc(a.ServiceName, username, func(style pam.Style, _ string) (string, error) {
		switch style {
		case pam.PromptEchoOff, pam.PromptEchoOn:
			return password, nil
		}
		return "", nil
	})
	if err != nil {
		return fmt.Errorf("pam_start: %w", err)
	}
	defer func() { _ = tx.End() }()

	if err := tx.Authenticate(0); err != nil {
		return fmt.Errorf("pam_authenticate: %w", err)
	}
	if err := tx.AcctMgmt(0); err != nil {
		return fmt.Errorf("pam_acct_mgmt: %w", err)
	}
	return nil
}
