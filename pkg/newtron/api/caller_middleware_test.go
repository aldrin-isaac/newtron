package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
)

// peerCredCtx attaches a *httputil.PeerCred to ctx the same way
// httputil.connContext does at the connection layer. Tests use this
// to simulate "this request arrived on a Unix socket listener with
// these peer credentials" without standing up a real Unix socket.
func peerCredCtx(ctx context.Context, uid uint32) context.Context {
	// Use the public API to set the PeerCred. We can't access the
	// private context key directly; httputil.PeerCredFromContext
	// reads via the same key the connContext hook sets. The
	// test-side setter is named accordingly and lives in httputil.
	return httputil.WithPeerCredForTest(ctx, &httputil.PeerCred{UID: uid, PID: 1})
}

// TestCallerMiddleware_PeerCredYieldsVerifiedCaller checks the Unix-
// socket path: when PeerCred is on the request context, the caller
// is built from getpwuid resolution with VerificationUnixPeerCreds.
// Even when the UID has no /etc/passwd entry (highly unlikely UID),
// the username falls back to "uid=N" so the audit log isn't blank.
func TestCallerMiddleware_PeerCredYieldsVerifiedCaller(t *testing.T) {
	var gotCaller *audit.Caller
	handler := callerMiddleware("X-Newtron-Caller")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCaller = audit.CallerFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	// A guaranteed-not-present UID — 32-bit max minus a small offset
	// — so user.LookupId returns nothing and we exercise the
	// uid=N fallback path.
	const syntheticUID = uint32(4294967294)
	req = req.WithContext(peerCredCtx(req.Context(), syntheticUID))
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotCaller == nil {
		t.Fatal("expected a caller to be attached; got nil")
	}
	if gotCaller.Source != audit.VerificationUnixPeerCreds {
		t.Errorf("Source = %q, want %q", gotCaller.Source, audit.VerificationUnixPeerCreds)
	}
	want := "uid=" + strconv.FormatUint(uint64(syntheticUID), 10)
	if gotCaller.Username != want {
		t.Errorf("Username = %q, want %q", gotCaller.Username, want)
	}
}

// TestCallerMiddleware_HeaderFallback_TCP checks the TCP fallback
// path: no PeerCred on the context, but a non-empty headerName
// configured AND the header set on the request — the caller is
// built from the header value with VerificationSelfAttestedHeader.
func TestCallerMiddleware_HeaderFallback_TCP(t *testing.T) {
	var gotCaller *audit.Caller
	handler := callerMiddleware("X-Newtron-Caller")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCaller = audit.CallerFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-Newtron-Caller", "alice")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotCaller == nil {
		t.Fatal("expected caller from header; got nil")
	}
	if gotCaller.Username != "alice" {
		t.Errorf("Username = %q, want %q", gotCaller.Username, "alice")
	}
	if gotCaller.Source != audit.VerificationSelfAttestedHeader {
		t.Errorf("Source = %q, want %q", gotCaller.Source, audit.VerificationSelfAttestedHeader)
	}
}

// TestCallerMiddleware_NoSourcesYieldsNoCaller checks the disabled
// state: empty headerName AND no PeerCred — no caller attached. The
// audit middleware downstream records User="" with VerificationUnknown.
func TestCallerMiddleware_NoSourcesYieldsNoCaller(t *testing.T) {
	var gotCaller *audit.Caller
	handler := callerMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCaller = audit.CallerFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	// Even if the client sets the canonical header, it gets ignored
	// because headerName is empty — exercising the "disabled" toggle.
	req.Header.Set("X-Newtron-Caller", "spoofed")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotCaller != nil {
		t.Errorf("expected no caller; got %+v", gotCaller)
	}
}

// TestCallerMiddleware_PeerCredWinsOverHeader pins the precedence:
// when a request carries both verified PeerCred and a self-attested
// header, the verified path wins. The header is the spoofable
// channel; the PeerCred is the kernel-attested one. Audit logs must
// reflect the verified source.
func TestCallerMiddleware_PeerCredWinsOverHeader(t *testing.T) {
	var gotCaller *audit.Caller
	handler := callerMiddleware("X-Newtron-Caller")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCaller = audit.CallerFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-Newtron-Caller", "spoofed-alice")
	req = req.WithContext(peerCredCtx(req.Context(), uint32(0))) // root uid
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotCaller == nil {
		t.Fatal("expected caller; got nil")
	}
	if gotCaller.Source != audit.VerificationUnixPeerCreds {
		t.Errorf("Source = %q, want %q (header must not override verified peer creds)",
			gotCaller.Source, audit.VerificationUnixPeerCreds)
	}
	// Username should be "root" or "uid=0" depending on whether
	// /etc/passwd has uid 0 (typically yes). Either way it should
	// NOT be the spoofed header value.
	if gotCaller.Username == "spoofed-alice" {
		t.Errorf("Username = %q, want NOT the spoofed header value", gotCaller.Username)
	}
}
