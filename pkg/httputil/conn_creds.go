package httputil

import (
	"context"
	"net"
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

// connContext is the *http.Server.ConnContext hook installed by
// Server.Start when a Unix socket listener is configured. For
// Unix-socket connections it pulls SO_PEERCRED off the underlying
// file descriptor and stores a *PeerCred in the per-connection
// context. For TCP connections it returns ctx unmodified; the
// downstream middleware then falls back to the header path.
//
// Errors from peer-cred extraction are swallowed and logged at the
// debug level — Unix-socket connections that don't yield credentials
// (a kernel that doesn't support SO_PEERCRED, an unusual socket
// option configuration) fall through to "no PeerCred on context" and
// the middleware treats the request as if it came from TCP. This is
// safer than failing the connection: the audit log will record the
// request as self-attested (or no-caller-attached) rather than
// dropping it silently.
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
