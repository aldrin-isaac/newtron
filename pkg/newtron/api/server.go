package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	netpkg "github.com/aldrin-isaac/newtron/pkg/newtron/network"
	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// PortResolver is newtron's public contract for resolving runtime
// SSH port allocations at Device.Connect time. It is a type alias
// for the internal sonic.PortResolver so external callers (cmd,
// tests) reference only newtron's public API surface (DESIGN_PRINCIPLES
// §33). The newtlab-backed implementation lives in pkg/newtlab/client
// and satisfies this contract structurally.
type PortResolver = sonic.PortResolver

// Server is the HTTP API server for newtron. The HTTP listener
// lifecycle (Start / Stop) comes from the embedded *httputil.Server;
// this type holds only newtron-specific state.
type Server struct {
	*httputil.Server

	mu       sync.RWMutex
	networks map[string]*networkEntity

	idleTimeout time.Duration
	logger      *log.Logger

	// portResolver supplies per-device SSH port allocations at
	// Connect time. Composed in from cmd/ (the only layer that knows
	// which engine provides the implementation — newtlab today).
	// Nil disables resolver consultation (tests, real-hardware).
	portResolver PortResolver

	// labDeployed, when non-nil, reports whether a lab is deployed under a name —
	// the delete-network guard (see Config.LabDeployed). Nil → no lab lifecycle,
	// guard is a no-op.
	labDeployed func(ctx context.Context, name string) (bool, error)

	// networksBase is the on-disk root for every registered network's
	// spec directory. POST /networks resolves to
	// filepath.Join(networksBase, id) — the operator names the
	// topology; newtron picks the path (§27, §33). Auto-discovery at
	// boot scans the same root, so an id created via POST /networks
	// reappears in /newtron/v1/networks after a restart without a
	// re-register dance. Set via Config.NetworksBase which
	// cmd/newt-server reads from --networks-base (default "networks").
	networksBase string

	// auditCallerHeader is the TCP-fallback HTTP header name for
	// self-attested caller identity (auth-design.md L1). Read by
	// callerMiddleware. Empty disables header-based identity.
	auditCallerHeader string

	// secretStore is the operator-configured secret backend
	// (auth-design.md L0). Passed to LoadNetwork on every
	// RegisterNetwork / ReloadNetwork. nil keeps plaintext-only
	// spec behavior.
	secretStore secret.Store

	// platforms is the GLOBAL platforms registry — loaded once at
	// startup by cmd/newt-server (via spec.LoadPlatformsFromDir) and
	// handed to every Network on LoadNetwork. nil is safe (every
	// platform lookup returns not-found, which is OK for test fixtures
	// that don't reference platforms).
	platforms map[string]*spec.PlatformSpec

	// audit enables per-network audit logging (auth-design.md L1). When
	// true, each registered network gets a FileLogger writing to its own
	// folder (audit.Path(specDir)); the middleware routes mutation events
	// there and the GET /audit/* handlers read it. When false, emission
	// is off and those endpoints return 404. auditIntegrity adds the L6
	// hash chain (per network — one chain per file).
	audit          bool
	auditIntegrity bool

	// enforceAuthorization (auth-design.md L3) drives
	// EnableAuthorization on every RegisterNetwork / ReloadNetwork
	// path. false → checkPermission stays inert; pre-L3 behavior.
	enforceAuthorization bool

	// globalSuperUsers are super-users across every network (server-level),
	// layered above each network's own super_users when authorization is enabled.
	globalSuperUsers []string

	// enforceWriteControl gates executing network mutations on the per-network
	// write-control reservation. false → reservation endpoints work but
	// enforcement is a no-op.
	enforceWriteControl bool

	// watcher is the auth-design.md L6 revocation watcher. nil when
	// cfg.SpecWatch is false. When set, RegisterNetwork adds the
	// network dir; UnregisterNetwork removes it; on settled spec-file
	// changes the watcher calls back into ReloadNetwork.
	watcher *netpkg.SpecWatcher
}

// Config carries every knob NewServer accepts. Uses a struct rather
// than positional params so the auth-design.md layered work (L1
// audit log + Unix socket + header; L2a mTLS; L2b PAM; L3 enforce)
// can grow the surface without each layer's PR resignaturing
// NewServer. Mirrors the existing newtlab/api.Config pattern.
//
// All fields are optional. Zero values give the pre-L1 behavior:
// no audit log, no Unix socket, TCP-only, no auth enforcement.
type Config struct {
	// Logger is the server's structured logger. nil → log.Default().
	Logger *log.Logger

	// IdleTimeout controls how long SSH connections to devices
	// are cached between requests. 0 → DefaultIdleTimeout (5m).
	// Negative → disable caching (connect per request).
	IdleTimeout time.Duration

	// PortResolver supplies per-device SSH port allocations at
	// Device.Connect time. nil disables resolver consultation
	// (real-hardware deployments, tests). The newtlab-backed
	// implementation is constructed in cmd/ and injected here;
	// the api package itself does not know about newtlab
	// (DESIGN_PRINCIPLES §33, §34).
	PortResolver PortResolver

	// LabDeployed, when non-nil, reports whether a lab is deployed under the
	// given name — the guard the delete-network endpoint consults so it refuses
	// (409) while a lab still runs against a network. Injected by cmd/newt-server
	// via the newtlab loopback client, so the check reaches lab state through
	// newtlab (§27), never the ~/.newtlab files, and the api package stays
	// unaware of newtlab (§33). Nil in a standalone newtron deployment (no lab
	// lifecycle) — the guard is then a no-op.
	LabDeployed func(ctx context.Context, name string) (bool, error)

	// NetworksBase is the on-disk root under which every registered
	// network's spec directory lives. POST /newtron/v1/networks
	// resolves each registration to filepath.Join(NetworksBase, id) —
	// operators name a topology, the server owns the path (§27, §33).
	// Boot-time auto-discovery uses the same root: every
	// <NetworksBase>/<name>/topology.json triggers an auto-register on
	// start, so the operator-named "<id>" maps to a stable on-disk
	// slot across server restarts.
	//
	// Required. Empty disables registration entirely — the server
	// returns 500 on POST /newtron/v1/networks until a base is
	// configured. cmd/newt-server reads this from --networks-base
	// (default "networks").
	NetworksBase string

	// AuditCallerHeader is the HTTP header name read by
	// callerMiddleware on TCP listeners to extract the
	// self-attested caller identity (auth-design.md L1). Empty
	// disables header-based identity — Unix socket peer creds
	// still work if UnixSocketPath is configured. Recommended
	// value when enabled: "X-Newtron-Caller".
	AuditCallerHeader string

	// UnixSocketPath enables a Unix-domain socket listener
	// alongside the TCP one (auth-design.md L1). When set,
	// requests on the Unix listener carry verified peer
	// credentials extracted via SO_PEERCRED; the
	// caller-extraction middleware tags them with
	// VerificationUnixPeerCreds. Empty disables the Unix listener.
	UnixSocketPath string

	// SecretStore is the operator-configured secret backend
	// (auth-design.md L0). When non-nil, networks loaded through
	// RegisterNetwork / ReloadNetwork resolve ${secret:KEY}
	// references in nodeSpec and platform spec values. nil means no
	// resolution: plaintext spec values pass through, references
	// become hard errors at load. Composed in by cmd/newt-server
	// from a --secret-store=PATH flag.
	SecretStore secret.Store

	// Platforms is the global platforms registry. cmd/newt-server
	// loads it once at startup via spec.LoadPlatformsFromDir from
	// --platforms-base and passes it here. Every Network registered
	// against this Server reads the same map. nil is safe (test
	// fixtures that don't reference platforms).
	Platforms map[string]*spec.PlatformSpec

	// TLSConfig enables inter-service mTLS on the TCP listener
	// (auth-design.md L2a). Build with httputil.LoadServerTLSConfig
	// from the operator's --tls-cert / --tls-key / --client-ca flags.
	// nil keeps the default plain-HTTP listener — the disabled state.
	TLSConfig *tls.Config

	// EnforceAuthorization turns the 26 inert checkPermission calls
	// into live gates (auth-design.md L3). When true, every
	// registered network has EnableAuthorization called after
	// load, and denials surface as auth.PermissionError → HTTP 403.
	// When false (default), checkPermission remains a no-op —
	// pre-L3 behavior preserved per the §2.4 enable/disable
	// contract. Composed in from --enforce-authorization on
	// cmd/newt-server.
	EnforceAuthorization bool

	// GlobalSuperUsers are super-users across every registered network, layered
	// above each network.json's own super_users — a global super-user bypasses
	// every permission check on every network without being named in any
	// network.json. Composed in from --super-users / NEWTRON_SUPER_USERS on
	// cmd/newt-server. Only meaningful with EnforceAuthorization.
	GlobalSuperUsers []string

	// EnforceWriteControl gates every executing network mutation on the
	// per-network write-control reservation: a caller must hold control (via
	// POST .../control/request) before any write, else 409. Default-closed when
	// on — a write with no holder is refused. When false (default) the
	// reservation endpoints still work but enforcement is a no-op, so existing
	// clients/scripts that don't claim are unchanged. Composed in from
	// --enforce-write-control on cmd/newt-server.
	EnforceWriteControl bool

	// SpecWatch enables the auth-design.md L6 revocation watcher.
	// When true, the server installs an fsnotify-backed watcher
	// on every RegisterNetwork's specDir; on settled file changes
	// (1s debounce) it invokes ReloadNetwork for the affected
	// network. Removing a grant from network.json then takes
	// effect within the debounce window without an explicit
	// /reload call. When false (default), the operator must POST
	// /reload to make a spec change observable.
	SpecWatch bool

	// Audit enables per-network audit logging (auth-design.md L1).
	// Composed in by cmd/newt-server from --audit. When true, every
	// registered network gets a FileLogger in its own folder
	// (audit.Path(specDir)); mutation and decision events route there,
	// and the GET /audit/* handlers read it. When false, emission is off
	// and those endpoints return 404.
	Audit bool

	// AuditIntegrity engages the L6 hash chain on each per-network audit
	// log (one chain per network file). From cmd/newt-server's
	// --audit-integrity; only meaningful with Audit.
	AuditIntegrity bool
}

// NewServer creates a new API server with the given Config. Zero-
// valued Config preserves the pre-L1 behavior (TCP-only, no audit
// log, no enforcement).
func NewServer(cfg Config) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	s := &Server{
		networks:             make(map[string]*networkEntity),
		idleTimeout:          idleTimeout,
		logger:               logger,
		portResolver:         cfg.PortResolver,
		labDeployed:          cfg.LabDeployed,
		networksBase:         cfg.NetworksBase,
		auditCallerHeader:    cfg.AuditCallerHeader,
		secretStore:          cfg.SecretStore,
		platforms:            cfg.Platforms,
		audit:                cfg.Audit,
		auditIntegrity:       cfg.AuditIntegrity,
		enforceAuthorization: cfg.EnforceAuthorization,
		globalSuperUsers:     cfg.GlobalSuperUsers,
		enforceWriteControl:  cfg.EnforceWriteControl,
	}
	if len(cfg.GlobalSuperUsers) > 0 {
		// Audit trail: who holds cross-network super-user is recorded at startup.
		logger.Printf("global super-users (all networks): %v", cfg.GlobalSuperUsers)
	}
	if cfg.SpecWatch {
		w, err := netpkg.NewSpecWatcher(logger, 0, func(id string) error {
			return s.ReloadNetwork(id)
		})
		if err != nil {
			logger.Printf("spec-watcher: disabled (init failed): %v", err)
		} else {
			s.watcher = w
		}
	}
	s.Server = httputil.NewServer(s.buildMux(), logger,
		httputil.ServerLabel("newtron-server"),
		// newtron handlers can do long device-facing operations; a
		// finite write timeout caps them. Different from newtrun /
		// newtlab which keep WriteTimeout=0 for SSE.
		httputil.WriteTimeout(5*time.Minute),
		httputil.UnixSocketPath(cfg.UnixSocketPath),
		httputil.TLSConfig(cfg.TLSConfig),
		httputil.OnShutdown(func() {
			if s.watcher != nil {
				s.watcher.Stop()
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, entity := range s.networks {
				entity.stop()
			}
			s.networks = make(map[string]*networkEntity)
		}),
	)
	if s.watcher != nil {
		s.watcher.Start(context.Background())
	}
	return s
}

// Handler returns the fully-wired http.Handler. Used by newt-server
// to mount newtron under /newtron/v1/ in the aggregated process and
// by tests that mount the server into httptest.Server without
// binding a real port.
func (s *Server) Handler() http.Handler {
	return s.HTTPServer().Handler
}

// CreateNetwork is the high-level operator API matching POST
// /newtron/v1/networks: name a network by id and the server resolves
// the on-disk dir, creates the empty spec layout if needed, and
// registers the result. Idempotent — calling twice with the same id
// is a no-op success.
//
// Description seeds topology.json when the slot is empty. Ignored
// when the slot already carries specs (no rewrite of authored files).
func (s *Server) CreateNetwork(id, description string) error {
	if s.networksBase == "" {
		return fmt.Errorf("server has no networks-base configured; cannot resolve dir for id %q", id)
	}
	// The archive store is a reserved subdirectory of the networks tree, not a
	// network — reject it as an id so it can never collide with a real network
	// (§15: the create path rejects what auto-discovery skips).
	if spec.IsReservedNetworkName(id) {
		return &newtron.ValidationError{Field: "id", Message: fmt.Sprintf("%q is a reserved name (the archive store)", id)}
	}
	dir := filepath.Join(s.networksBase, id)

	s.mu.Lock()
	_, exists := s.networks[id]
	s.mu.Unlock()
	if exists {
		return nil
	}

	if !dirHasSpecs(dir) {
		if err := spec.CreateEmpty(dir, description); err != nil && !errors.Is(err, spec.ErrAlreadyExists) {
			return err
		}
	}
	return s.RegisterNetwork(id, dir)
}

// RegisterNetwork loads a network from specDir and registers it under
// id. Lower-level than CreateNetwork — does no scaffolding; the dir
// must already carry a valid spec layout. Idempotent on the id: if
// it's already registered, returns nil. Used by auto-discovery at
// startup (cmd/newt-server/discover.go) and by tests that fixture
// arbitrary dirs.
// openAuditLogger opens a network's audit logger at audit.Path(specDir)
// when audit is enabled, else returns a nil Logger. A failure to open (an
// unwritable spec dir, say) is logged and treated as "no audit for this
// network" rather than failing registration — one network's audit problem
// must not take down the server or block its peers.
func (s *Server) openAuditLogger(specDir string) audit.Logger {
	if !s.audit {
		return nil
	}
	path := audit.Path(specDir)
	var (
		l   *audit.FileLogger
		err error
	)
	if s.auditIntegrity {
		l, err = audit.NewFileLoggerWithIntegrity(path, audit.RotationConfig{})
	} else {
		l, err = audit.NewFileLogger(path, audit.RotationConfig{})
	}
	if err != nil {
		s.logger.Printf("audit: cannot open log at %s: %v (auditing disabled for this network)", path, err)
		return nil
	}
	return l
}

// auditLoggerFor returns the audit logger for a request's network, or nil
// when audit is off, the network isn't registered, or the request carried
// no {netID} (e.g. POST /networks — a server-registry lifecycle act, not a
// network-scoped mutation). The mutation middleware calls this per request;
// a nil result is a silent no-op.
func (s *Server) auditLoggerFor(netID string) audit.Logger {
	if netID == "" {
		return nil
	}
	if ne := s.getNetwork(netID); ne != nil {
		return ne.auditLogger
	}
	return nil
}

func (s *Server) RegisterNetwork(id, specDir string) error {
	net, err := newtron.LoadNetwork(specDir, networkName(specDir), s.portResolver, s.secretStore, s.platforms)
	if err != nil {
		return fmt.Errorf("loading network from %s: %w", specDir, err)
	}
	if s.enforceAuthorization {
		net.EnableAuthorization(id, s.globalSuperUsers...)
	}
	auditLogger := s.openAuditLogger(specDir)
	net.SetAuditLogger(auditLogger)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.networks[id]; exists {
		// Idempotent no-op — close the logger we just opened so its file
		// handle doesn't leak (the already-registered entity owns the live one).
		if auditLogger != nil {
			_ = auditLogger.Close()
		}
		return nil
	}
	s.networks[id] = newNetworkEntity(net, specDir, s.idleTimeout, auditLogger)
	s.logger.Printf("registered network '%s' from %s", id, specDir)
	if s.watcher != nil {
		if err := s.watcher.Add(specDir, id); err != nil {
			s.logger.Printf("spec-watcher: cannot watch %s for network '%s': %v", specDir, id, err)
		}
	}
	return nil
}

// UnregisterNetwork removes a registered network. Stops all NodeActors
// (draining in-flight requests and closing SSH connections) before removing.
func (s *Server) UnregisterNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entity, exists := s.networks[id]
	if !exists {
		return fmt.Errorf("network '%s' not registered", id)
	}

	entity.stop()
	delete(s.networks, id)
	if s.watcher != nil {
		_ = s.watcher.Remove(entity.specDir)
	}
	s.logger.Printf("unregistered network '%s'", id)
	return nil
}

// authorizeRegistry gates the server-level network-registry acts (create,
// delete) at the global super-user set. These acts scaffold or archive a whole
// network's spec directory — an operator/registry authority, not a per-network
// spec write — and delete targets an UNREGISTERED network (no per-network
// permission model is loaded), so the gate is the server-wide --super-users list,
// not any network.json. action is the label recorded on a denial (e.g.
// "network.create"). A no-op when enforcement is disabled, mirroring the
// per-network checkPermission gates.
func (s *Server) authorizeRegistry(ctx context.Context, action string) error {
	if !s.enforceAuthorization {
		return nil
	}
	var caller string
	if c := audit.CallerFromContext(ctx); c != nil {
		caller = c.Username
	}
	if slices.Contains(s.globalSuperUsers, caller) {
		return nil
	}
	return &newtron.AuthorizationError{Caller: caller, Permission: action, Resource: "network"}
}

// DeleteNetwork soft-deletes the network `id` by MOVING its spec directory to the
// archive store (<networksBase>/archives/<id>-<timestamp>) — the on-disk reverse
// of CreateNetwork's scaffold (§15). Nothing is erased: the specs, secrets.json,
// and audit log travel to the archive intact, so the delete is undoable, but only
// by manually moving the archived directory back. The archive is invisible to the
// API (auto-discovery skips the reserved dir; list-networks is in-memory).
//
// This is the EXISTENCE layer only — it does not touch the serving layer. The two
// are kept separate: a network must be UNREGISTERED first (POST .../unregister),
// because the running server holds the dir open (audit handle, fs-watch, live
// SSH) and archiving it out from under a live entity would dangle those. So:
//   - still registered → *util.ConflictError (409): "unregister first";
//   - not on disk → *newtron.NotFoundError (404);
//   - unless force, a lab deployed under this name blocks the delete
//     (*util.ConflictError → 409) — destroy the lab first. A lab-check error
//     fails closed: the delete is refused, never forced through on uncertainty.
//
// The caller (the handler) is responsible for the registry authorization gate.
// timestamp is the archive suffix (a UTC stamp like "20060102T150405Z"), passed
// in so the method stays clock-free and testable. Returns the archive path.
func (s *Server) DeleteNetwork(ctx context.Context, id string, force bool, timestamp string) (string, error) {
	if s.networksBase == "" {
		return "", fmt.Errorf("server has no networks-base configured; cannot resolve dir for id %q", id)
	}
	// Serving layer must be torn down first — an explicit precondition, not a
	// hidden side effect (Register/Unregister and Create/Delete are separate).
	s.mu.RLock()
	_, registered := s.networks[id]
	s.mu.RUnlock()
	if registered {
		return "", &util.ConflictError{
			Resource:   "network",
			Name:       id,
			References: []string{"network is still registered — POST /newtron/v1/networks/" + id + "/unregister first"},
		}
	}

	dir := filepath.Join(s.networksBase, id)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", &newtron.NotFoundError{Resource: "network", Name: id}
	}

	// Lab guard — reached through newtlab (§27), never the ~/.newtlab files.
	if !force && s.labDeployed != nil {
		deployed, err := s.labDeployed(ctx, id)
		if err != nil {
			return "", fmt.Errorf("checking whether a lab is deployed under %q: %w", id, err)
		}
		if deployed {
			return "", &util.ConflictError{
				Resource:   "network",
				Name:       id,
				References: []string{fmt.Sprintf("lab %q is deployed — run `newtlab destroy %s` first", id, id)},
				Force:      true,
			}
		}
	}

	archived, err := spec.ArchiveNetwork(s.networksBase, id, timestamp)
	if err != nil {
		return "", fmt.Errorf("archiving network %q: %w", id, err)
	}
	s.logger.Printf("deleted network '%s' (archived → %s)", id, archived)
	return archived, nil
}

// ReloadNetwork stops the existing networkEntity, reloads specs from disk,
// and creates a fresh networkEntity. SSH connections reconnect lazily on
// next request.
func (s *Server) ReloadNetwork(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entity, exists := s.networks[id]
	if !exists {
		return &notRegisteredError{id}
	}

	// Drain the old entity's node actors, but KEEP its audit logger open —
	// reload changes specs, not the audit ledger. The logger is carried to the
	// new entity so the network's hash chain is continuous and no in-flight
	// mutation loses its event to a close/reopen race.
	entity.stopNodes()

	// Reload specs from disk
	net, err := newtron.LoadNetwork(entity.specDir, networkName(entity.specDir), s.portResolver, s.secretStore, s.platforms)
	if err != nil {
		return fmt.Errorf("reloading specs from %s: %w", entity.specDir, err)
	}
	if s.enforceAuthorization {
		net.EnableAuthorization(id, s.globalSuperUsers...)
	}
	net.SetAuditLogger(entity.auditLogger)

	// Replace with new entity, carrying the same audit logger forward.
	s.networks[id] = newNetworkEntity(net, entity.specDir, s.idleTimeout, entity.auditLogger)
	s.logger.Printf("reloaded network '%s' from %s", id, entity.specDir)
	return nil
}

// getNetwork returns the networkEntity for the given ID, or nil.
func (s *Server) getNetwork(id string) *networkEntity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.networks[id]
}

// ListNetworks returns info about all registered networks. Exposed
// so cmd/newt-server can introspect the registry after auto-discovery
// for tests + startup banners; the HTTP list handler calls the same
// method (§27 Single Owner — no duplicate "what's registered?" logic).
func (s *Server) ListNetworks() []NetworkInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]NetworkInfo, 0, len(s.networks))
	for id, entity := range s.networks {
		result = append(result, networkInfoFor(id, entity))
	}
	return result
}

// getNetworkInfo returns NetworkInfo for the registered id, or nil
// when no network is registered under that id. Used by the
// register-network handler to return the canonical NetworkInfo on 201
// (§46) so the client learns the resolved dir even when the
// server picked it (#122).
func (s *Server) getNetworkInfo(id string) *NetworkInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entity, ok := s.networks[id]
	if !ok {
		return nil
	}
	info := networkInfoFor(id, entity)
	return &info
}

// networkInfoFor projects a single registered networkEntity into the
// canonical wire shape. Single source of truth for the projection so
// the list path and the per-id path never diverge.
func networkInfoFor(id string, entity *networkEntity) NetworkInfo {
	info := NetworkInfo{
		ID:          id,
		Dir:         entity.specDir,
		HasTopology: entity.net.HasTopology(),
		Topology:    networkName(entity.specDir),
		Nodes:       entity.net.ListNodes(),
	}
	if wc, held := entity.controlStatus(); held {
		info.WriteControl = &WriteControlInfo{Holder: wc.Holder, Since: wc.Since, ExpiresAt: wc.ExpiresAt}
	}
	return info
}

// networkName derives the network name from its directory path.
// After the layout collapse, dir IS the network root, so the name
// is its basename.
// e.g. "networks/1node-vs" → "1node-vs"
func networkName(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	if base == "." || base == "/" {
		return ""
	}
	return base
}
