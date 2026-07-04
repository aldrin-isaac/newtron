package httputil

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Server is the embedded base type each newtron-project HTTP server
// composes into its own Server struct. It owns the *http.Server, the
// shared listen log line, the lifecycle (Start / Stop), and the
// pre-shutdown hook each engine uses to cancel its in-flight work
// (newtrun's RunRegistry.CancelAll, newtlab's LabOpRegistry.CancelAll).
//
// Engine-specific Server types embed *httputil.Server and pass the
// fully-composed http.Handler (mux + middleware chain) at construction
// time:
//
//	type Server struct {
//	    *httputil.Server
//	    cfg Config
//	    broker *httputil.Broker[Event]
//	    registry *LabOpRegistry
//	}
//
//	func NewServer(cfg Config) *Server {
//	    s := &Server{ ... }
//	    s.Server = httputil.NewServer(s.buildHandler(), cfg.Logger,
//	        httputil.ServerLabel("newtlab-server"),
//	        httputil.OnShutdown(func() {
//	            s.registry.CancelAll(5 * time.Second)
//	        }),
//	    )
//	    return s
//	}
//
// Why embed instead of compose: every server's Start / Stop forwarders
// would be identical wrappers; embedding makes them disappear. Engine-
// specific fields and methods live on the outer type; lifecycle is
// inherited.
type Server struct {
	httpServer *http.Server
	logger     *log.Logger
	label      string // log-prefix label, e.g. "newtrun-server"
	onShutdown []func()

	// unixSocketPath is the optional Unix-domain socket listener
	// path (auth-design.md L1). When set, Start binds both a TCP
	// listener on its addr argument AND a Unix socket listener on
	// this path; the request's identity-extraction middleware
	// uses the listener type to decide between SO_PEERCRED (Unix)
	// and a self-attested header (TCP).
	//
	// Empty disables the Unix socket listener — TCP only.
	unixSocketPath string

	// unixListener is the active Unix socket listener when
	// unixSocketPath is set. Held so Stop can close it during
	// graceful shutdown without leaking the socket file.
	unixListener net.Listener

	// unixServeErr captures the Unix listener's serve error so
	// Start can return it after TCP shutdown completes. Mutexed
	// because two goroutines (TCP serve and Unix serve) write
	// completion state concurrently.
	unixServeErr   error
	unixServeErrMu sync.Mutex

	// tlsConfig is the optional inter-service mTLS configuration
	// (auth-design.md L2a). When non-nil, the TCP listener is
	// wrapped with tls.NewListener and the connContext hook
	// extracts the verified peer cert CN from each connection
	// (parallel to SO_PEERCRED extraction from Unix-socket
	// connections — L1). nil means plain HTTP, which is the
	// default behavior and preserves the pre-L2a posture exactly.
	tlsConfig *tls.Config
}

// ServerOption tunes the base server at construction time.
type ServerOption func(*serverConfig)

type serverConfig struct {
	label          string
	readTimeout    time.Duration
	writeTimeout   time.Duration
	idleTimeout    time.Duration
	onShutdown     []func()
	unixSocketPath string
	tlsConfig      *tls.Config
}

// ServerLabel sets the prefix used in the startup log line. Default is
// "http-server"; engines pass their own ("newtrun-server", etc.) so
// "<label> listening on <addr>" reads correctly.
func ServerLabel(s string) ServerOption {
	return func(c *serverConfig) { c.label = s }
}

// WriteTimeout overrides the *http.Server WriteTimeout. Default 0
// (no per-request write deadline) so SSE handlers can hold long-lived
// connections without the server-wide timeout killing them. Engines
// that need a finite write timeout (newtron-server uses 5min) pass
// it explicitly.
func WriteTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.writeTimeout = d }
}

// OnShutdown registers a function to run before the HTTP listener
// shuts down. Engines use it to cancel in-flight goroutines they own
// (RunRegistry.CancelAll, LabOpRegistry.CancelAll). Multiple hooks
// run in registration order. The HTTP listener then takes whatever
// time remains in the context to drain.
func OnShutdown(fn func()) ServerOption {
	return func(c *serverConfig) { c.onShutdown = append(c.onShutdown, fn) }
}

// UnixSocketPath enables a Unix-domain socket listener alongside the
// TCP listener (auth-design.md L1). Empty disables the Unix listener
// — TCP only. The path is created at Start and removed at Stop;
// existing files at the path are removed first so a stale socket
// from a previous run doesn't block startup.
//
// When set, both listeners serve the same http.Handler; the
// identity-extraction middleware in pkg/newtron/api/ distinguishes
// requests by listener type (LocalAddr().Network() returns "unix"
// for Unix-socket connections, "tcp" for TCP).
func UnixSocketPath(path string) ServerOption {
	return func(c *serverConfig) { c.unixSocketPath = path }
}

// TLSConfig enables inter-service mTLS on the TCP listener
// (auth-design.md L2a). Build the *tls.Config with
// httputil.LoadServerTLSConfig; pass nil to keep the default plain-
// HTTP listener (the L2a disabled state).
//
// When the config has ClientAuth = tls.RequireAndVerifyClientCert
// (set by LoadServerTLSConfig when a clientCAFile is provided),
// every TCP connection completes a mTLS handshake and the verified
// peer cert CN flows into the request context via connContext for
// the identity-extraction middleware to read.
//
// TLS applies to TCP only. The Unix socket listener, when also
// configured, stays plain — OS-level peer credentials already
// provide verified identity on the Unix path.
func TLSConfig(cfg *tls.Config) ServerOption {
	return func(c *serverConfig) { c.tlsConfig = cfg }
}

// NewServer constructs a base server. handler is the
// already-middleware-wrapped http.Handler the engine wants to serve.
// logger may be nil; if so, log.Default() is used.
func NewServer(handler http.Handler, logger *log.Logger, opts ...ServerOption) *Server {
	cfg := serverConfig{
		label:        "http-server",
		readTimeout:  30 * time.Second,
		writeTimeout: 0,
		idleTimeout:  120 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{
		logger:         logger,
		label:          cfg.label,
		onShutdown:     cfg.onShutdown,
		unixSocketPath: cfg.unixSocketPath,
		tlsConfig:      cfg.tlsConfig,
		httpServer: &http.Server{
			Handler:      handler,
			ReadTimeout:  cfg.readTimeout,
			WriteTimeout: cfg.writeTimeout,
			IdleTimeout:  cfg.idleTimeout,
			TLSConfig:    cfg.tlsConfig,
		},
	}
	// Install the connContext hook whenever a verified-identity
	// source is configured. The hook is the join point for L1's
	// Unix-socket SO_PEERCRED and L2a's TLS peer-cert-CN
	// extraction; with either source enabled, every request
	// carries the verified identity in its context for the
	// downstream identity middleware to read. TCP-without-TLS
	// requests pass through unchanged either way.
	if cfg.unixSocketPath != "" || cfg.tlsConfig != nil {
		s.httpServer.ConnContext = connContext
	}
	return s
}

// Start begins listening on addr (TCP). When the Server was
// configured with a non-empty UnixSocketPath, Start also binds a
// Unix-domain socket listener at that path; both listeners serve the
// same handler. Blocks until the server stops.
//
// Returns nil on graceful shutdown (http.ErrServerClosed from both
// listeners); the first non-shutdown error from either listener
// otherwise.
func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr

	if s.unixSocketPath != "" {
		// Remove a stale socket file from a previous run so the
		// bind succeeds. If the path exists and is not a socket
		// (e.g., a regular file the operator created by mistake),
		// the Listen call below surfaces the EADDRINUSE-like
		// error with the path in it — better diagnostics than
		// silently succeeding.
		_ = os.Remove(s.unixSocketPath)
		ln, err := net.Listen("unix", s.unixSocketPath)
		if err != nil {
			return err
		}
		s.unixListener = ln
		s.logger.Printf("%s listening on %s (unix)", s.label, s.unixSocketPath)
		go func() {
			serveErr := s.httpServer.Serve(ln)
			if !errors.Is(serveErr, http.ErrServerClosed) {
				s.unixServeErrMu.Lock()
				s.unixServeErr = serveErr
				s.unixServeErrMu.Unlock()
			}
		}()
	}

	tcpProto := "http"
	if s.tlsConfig != nil {
		tcpProto = "https"
	}
	s.logger.Printf("%s listening on %s (%s)", s.label, addr, tcpProto)

	var err error
	if s.tlsConfig != nil {
		// Hand-rolled TLS path so the wrapping listener and the
		// connContext hook see *tls.Conn directly. Using
		// ListenAndServeTLS hides the conn type behind the http
		// framework and breaks cert-CN extraction in connContext.
		tcpLn, lerr := net.Listen("tcp", addr)
		if lerr != nil {
			err = lerr
		} else {
			tlsLn := tls.NewListener(tcpLn, s.tlsConfig)
			err = s.httpServer.Serve(tlsLn)
		}
	} else {
		err = s.httpServer.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	// Prefer the TCP error if any; otherwise surface the Unix
	// listener's error captured by its goroutine.
	if err == nil {
		s.unixServeErrMu.Lock()
		err = s.unixServeErr
		s.unixServeErrMu.Unlock()
	}
	return err
}

// Stop runs every OnShutdown hook in registration order, then
// gracefully shuts down both listeners (TCP and, if configured, the
// Unix socket) with the given context as the drain deadline. The
// Unix socket file is removed after the listener closes so the next
// Start doesn't have to rely on the stale-file cleanup at the head
// of its bind sequence.
func (s *Server) Stop(ctx context.Context) error {
	for _, fn := range s.onShutdown {
		fn()
	}
	err := s.httpServer.Shutdown(ctx)
	if s.unixListener != nil {
		// Closing the listener unblocks its Serve goroutine; the
		// http.Server.Shutdown above already drained any
		// in-flight Unix-socket requests because both listeners
		// share the same *http.Server.
		_ = s.unixListener.Close()
		_ = os.Remove(s.unixSocketPath)
	}
	return err
}

// HTTPServer exposes the underlying *http.Server. Tests use it to read
// configured timeouts; engines should not need it.
func (s *Server) HTTPServer() *http.Server { return s.httpServer }
