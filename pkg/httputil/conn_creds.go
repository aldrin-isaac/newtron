package httputil

import (
	"context"
	"net"
	"net/http"
	"syscall"
)

// PeerCred is the verified per-connection identity for a Unix-domain
// socket peer (auth-design.md L1). UID is the kernel-attested user ID
// of the connecting process; PID is its process ID. The audit layer
// resolves UID to a username via getpwuid; the username carries the
// VerificationUnixPeerCreds verification source.
//
// Populated by Server.connContext when UnixSocketPath is configured.
// Available on the request context via PeerCredFromContext.
type PeerCred struct {
	UID uint32
	PID int32
}

// peerCredKey is the request-context key under which PeerCred is
// stored. Unexported so the only valid setter is connContext below.
type peerCredKey struct{}

// PeerCredFromContext returns the verified Unix-socket peer credentials
// attached to ctx by the server's ConnContext hook, or nil when the
// request came from a TCP listener (or any other non-Unix connection).
// Identity-extraction middleware uses this to decide between
// "verified via SO_PEERCRED" and "fall through to TCP header."
func PeerCredFromContext(ctx context.Context) *PeerCred {
	if ctx == nil {
		return nil
	}
	pc, _ := ctx.Value(peerCredKey{}).(*PeerCred)
	return pc
}

// WithPeerCredForTest attaches pc to ctx using the same internal key
// PeerCredFromContext reads. Test-only: production code receives
// PeerCred through the connContext hook on a real Unix-socket
// connection, never through this setter. Exposed so middleware tests
// in sibling packages can simulate "this request arrived on a Unix
// socket with these credentials" without standing up a real Unix
// listener.
func WithPeerCredForTest(ctx context.Context, pc *PeerCred) context.Context {
	return context.WithValue(ctx, peerCredKey{}, pc)
}

// ServiceCertCN is the verified Common Name from the peer's X.509
// client certificate on an mTLS-protected connection (auth-design.md
// L2a). Populated by Server.connContext when the connection is a
// *tls.Conn whose verified peer cert chain is non-empty.
//
// Used by inter-service callers (engine → engine) where both sides
// hold certs from a shared CA. The audit layer tags the request
// with VerificationServiceCertCN; authorization (L3) treats
// service identities like any other principal in the entitlement
// pattern, with the spec convention that service CNs map to
// network.json super_users.
type ServiceCertCN string

// serviceCertCNKey is the request-context key under which the
// verified peer cert CN is stored. Unexported so the only valid
// setter is connContext.
type serviceCertCNKey struct{}

// ServiceCertCNFromContext returns the verified service cert CN
// attached by Server.connContext, or empty when the request did
// not arrive over an mTLS connection with a verified client cert.
// Identity-extraction middleware uses this to decide between
// "verified via cert CN" and "fall through to peer creds / header."
func ServiceCertCNFromContext(ctx context.Context) ServiceCertCN {
	if ctx == nil {
		return ""
	}
	cn, _ := ctx.Value(serviceCertCNKey{}).(ServiceCertCN)
	return cn
}

// WithServiceCertCNForTest attaches cn to ctx using the same key
// ServiceCertCNFromContext reads. Test-only: production code
// receives the value through the connContext hook on a real mTLS
// connection. Exposed so middleware tests in sibling packages can
// simulate "this request arrived over mTLS with this peer cert CN"
// without standing up a TLS listener + dialer pair.
func WithServiceCertCNForTest(ctx context.Context, cn ServiceCertCN) context.Context {
	return context.WithValue(ctx, serviceCertCNKey{}, cn)
}

// connContext is the *http.Server.ConnContext hook installed by
// Server.Start when verified-identity sources need per-connection
// extraction. Currently this covers the Unix-socket path: when the
// connection is a *net.UnixConn, the hook pulls SO_PEERCRED off the
// underlying file descriptor and stashes a *PeerCred on the
// per-connection context for the identity-extraction middleware
// (auth-design.md L1).
//
// The TLS path is NOT handled here. ConnContext fires before the
// TLS handshake completes, so VerifiedChains on the *tls.Conn is
// empty at that moment. Verified peer-cert identity (auth-design.md
// L2a) is extracted from r.TLS in the request middleware via
// ServiceCertCNFromRequest below — by which time the handshake has
// completed and VerifiedChains is populated.
//
// Errors from peer-cred extraction are swallowed: a Unix-socket
// connection whose SO_PEERCRED fails falls through to "no PeerCred
// on context" and the middleware treats the request as if it came
// over plain TCP. The audit log records the request with whichever
// lower-priority source the configuration provides.
func connContext(ctx context.Context, c net.Conn) context.Context {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return ctx
	}
	pc, err := peerCredFromUnixConn(uc)
	if err != nil || pc == nil {
		return ctx
	}
	return context.WithValue(ctx, peerCredKey{}, pc)
}

// ServiceCertCNFromRequest returns the verified peer cert CN from
// the request's TLS connection state, or empty when the request did
// not arrive over an mTLS connection with a verified client cert
// (auth-design.md L2a). Identity-extraction middleware in pkg/
// newtron/api/, pkg/newtlab/api/, and pkg/newtrun/api/ calls this
// to populate the request's audit.Caller with
// VerificationServiceCertCN before falling through to
// PeerCredFromContext or the self-attested header.
//
// The function is safe to call on any *http.Request; nil r and
// missing r.TLS both return empty.
func ServiceCertCNFromRequest(r *http.Request) ServiceCertCN {
	if r == nil || r.TLS == nil {
		return ""
	}
	if len(r.TLS.VerifiedChains) == 0 || len(r.TLS.VerifiedChains[0]) == 0 {
		return ""
	}
	return ServiceCertCN(r.TLS.VerifiedChains[0][0].Subject.CommonName)
}

// peerCredFromUnixConn extracts SO_PEERCRED from the underlying socket
// file descriptor of a Unix-domain socket connection. Linux-only per
// the project's Linux x86_64 target; the syscall.GetsockoptUcred API
// is the kernel-supported path for this on Linux.
func peerCredFromUnixConn(c *net.UnixConn) (*PeerCred, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	var ucred *syscall.Ucred
	var ucredErr error
	if controlErr := raw.Control(func(fd uintptr) {
		ucred, ucredErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); controlErr != nil {
		return nil, controlErr
	}
	if ucredErr != nil {
		return nil, ucredErr
	}
	return &PeerCred{UID: ucred.Uid, PID: ucred.Pid}, nil
}
