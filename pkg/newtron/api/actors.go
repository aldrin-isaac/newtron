package api

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// DefaultIdleTimeout is how long a device SSH connection stays cached after
// its last operation before being automatically closed.
const DefaultIdleTimeout = 5 * time.Minute

// ============================================================================
// Actor message types
// ============================================================================

// request is a message sent to an actor's channel.
type request struct {
	fn     func() (any, error)
	result chan response
}

// response is the result of processing a request.
type response struct {
	value any
	err   error
}

// ============================================================================
// networkEntity — one registered network's API-layer state
// ============================================================================

// networkEntity is the API layer's record for one registered network: it
// pairs the engine's *newtron.Network with the network directory it was loaded
// from and a cache of per-device NodeActors. The Server holds one of these
// per registered netID.
//
// This is NOT an actor — no goroutine, no message passing, no isolated
// state, no spec serialization. NodeActor IS an actor (cached transport +
// idle-timer driven by a select loop); the asymmetry is deliberate.
//
// Spec serialization lives at the engine layer. *newtron.Network's public
// Create/Update/Delete/Add/Remove methods are internally atomic — they
// compose check + mutate + persist under a single keyNetworkSpec.Lock (or
// keyTopology.Lock for topology methods). Reads (List/Show/Get/Snapshot)
// take the corresponding RLock and run concurrently. The API layer has no
// lock of its own around spec data; handlers call ne.net.X() directly.
// See docs/newtron/hld.md "Concurrency model" for the full ownership map.
type networkEntity struct {
	net         *newtron.Network
	specDir     string
	idleTimeout time.Duration

	// auditLogger is this network's audit logger (a *audit.FileLogger
	// writing to audit.Path(specDir)), or nil when audit is disabled.
	// Owned here for lifecycle — stop() closes it. The same instance is
	// handed to net (net.SetAuditLogger) for decision events and read by
	// the mutation middleware via Server.auditLoggerFor: one writer per
	// network file.
	auditLogger audit.Logger

	// nodeMu guards the nodeActors map. Not a spec lock — protects the
	// API layer's own runtime cache. handleServiceProjection snapshots
	// the registry directly through this mutex.
	nodeMu     sync.Mutex
	nodeActors map[string]*NodeActor

	// wcMu guards writeCtl, the per-network write-control reservation
	// (request/release/takeover). nil = free. In-memory only — a server
	// restart clears it, the clean reset for any stale reservation.
	wcMu     sync.Mutex
	writeCtl *writeControl
}

// newNetworkEntity creates a networkEntity for a registered network.
// auditLogger is the network's audit logger (nil when audit is disabled);
// the entity owns its lifecycle and closes it in stop().
func newNetworkEntity(net *newtron.Network, specDir string, idleTimeout time.Duration, auditLogger audit.Logger) *networkEntity {
	return &networkEntity{
		net:         net,
		specDir:     specDir,
		idleTimeout: idleTimeout,
		auditLogger: auditLogger,
		nodeActors:  make(map[string]*NodeActor),
	}
}

// getNodeActor returns or creates a NodeActor for the given device.
func (ne *networkEntity) getNodeActor(device string) *NodeActor {
	ne.nodeMu.Lock()
	defer ne.nodeMu.Unlock()
	if actor, ok := ne.nodeActors[device]; ok {
		return actor
	}
	actor := newNodeActor(ne.net, device, ne.idleTimeout)
	ne.nodeActors[device] = actor
	return actor
}

// removeNodeActor closes the NodeActor for device (if any) and drops it from
// the cache. Called by handlers that mutate or delete a topology device — the
// cached node is now stale and must be rebuilt from the new spec on next
// access.
func (ne *networkEntity) removeNodeActor(device string) {
	ne.nodeMu.Lock()
	actor, ok := ne.nodeActors[device]
	if ok {
		delete(ne.nodeActors, device)
	}
	ne.nodeMu.Unlock()
	if ok {
		actor.stop()
	}
}

// stop shuts down all NodeActors and drops the cache. The entity itself
// has no goroutine to wind down.
// stopNodes drains the node actors (in-flight requests, SSH connections)
// without touching the audit logger. ReloadNetwork uses this so the network's
// audit logger — and its open hash chain — survive a spec reload uninterrupted:
// closing and reopening it would (a) do pointless work and (b) open a race where
// an in-flight mutation's middleware, having already fetched the logger
// reference, writes to a just-closed file and loses the event.
func (ne *networkEntity) stopNodes() {
	ne.nodeMu.Lock()
	for _, nodeActor := range ne.nodeActors {
		nodeActor.stop()
	}
	ne.nodeActors = make(map[string]*NodeActor)
	ne.nodeMu.Unlock()
}

// stop drains node actors AND closes the audit logger — the terminal
// transitions (UnregisterNetwork, server shutdown) where the network's audit
// file should be released. ReloadNetwork uses stopNodes instead and carries the
// logger forward.
func (ne *networkEntity) stop() {
	ne.stopNodes()
	if ne.auditLogger != nil {
		_ = ne.auditLogger.Close()
	}
}

// ============================================================================
// NodeActor — serializes device operations for a single Node
// ============================================================================

// NodeActor serializes all operations on a single device. It caches the
// abstract node (via *newtron.Node) between requests. The node may be
// topology-sourced or actuated — mode switching is handled by execute().
//
// Architecture §10: "NodeActor.execute — single entry point." All mode
// dispatch happens in execute via one branch: ensureActuatedIntent vs
// ensureTopologyIntent. No handler, no CRUD operation bypasses this.
type NodeActor struct {
	net         *newtron.Network
	device      string
	idleTimeout time.Duration

	// Cached abstract node. May be topology-sourced or actuated.
	// Only accessed from the actor goroutine (run loop).
	node *newtron.Node

	requests chan request
	done     chan struct{}
}

// newNodeActor creates and starts a NodeActor.
func newNodeActor(net *newtron.Network, device string, idleTimeout time.Duration) *NodeActor {
	actor := &NodeActor{
		net:         net,
		device:      device,
		idleTimeout: idleTimeout,
		requests:    make(chan request, 64),
		done:        make(chan struct{}),
	}
	go actor.run()
	return actor
}

// run is the actor's event loop. It processes requests and closes the cached
// node after idleTimeout of inactivity.
func (na *NodeActor) run() {
	defer close(na.done)
	defer na.closeNode()

	idle := time.NewTimer(0)
	if !idle.Stop() {
		<-idle.C
	}

	for {
		select {
		case req, ok := <-na.requests:
			if !ok {
				idle.Stop()
				return
			}
			val, err := req.fn()
			req.result <- response{value: val, err: err}
			// Reset idle timer if we have a cached node.
			if na.node != nil {
				resetTimer(idle, na.idleTimeout)
			}
		case <-idle.C:
			log.Printf("newtron-server: closing idle connection to %s", na.device)
			na.closeNode()
		}
	}
}

// closeNode closes and discards the cached node.
// Must only be called from within the actor goroutine.
func (na *NodeActor) closeNode() {
	if na.node != nil {
		na.node.Close()
		na.node = nil
	}
}

// do sends a closure to the actor and waits for the result.
func (na *NodeActor) do(ctx context.Context, fn func() (any, error)) (any, error) {
	res := make(chan response, 1)
	select {
	case na.requests <- request{fn: fn, result: res}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-res:
		return r.value, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// stop shuts down the NodeActor.
func (na *NodeActor) stop() {
	close(na.requests)
	<-na.done
}

// ============================================================================
// Mode dispatch — the ONE branch point for all operations
// ============================================================================

// ensureActuatedIntent ensures the cached node was built from the device's
// own NEWTRON_INTENT records. If the current node is topology-sourced, it is
// destroyed and replaced with a node built from device intents.
//
// Architecture §3: "Topology offline → actuated online: ensureActuatedIntent
// closes the offline node, creates a new one from device intents."
//
// Guarded: refuses to destroy a topology node with unsaved CRUD mutations.
// The user must `intent save --topology` first or `intent reload --topology`
// to discard.
func (na *NodeActor) ensureActuatedIntent(ctx context.Context) error {
	if na.node != nil && na.node.HasActuatedIntent() {
		return nil // already actuated
	}
	if na.node != nil && na.node.HasUnsavedIntents() {
		return fmt.Errorf("topology node has unsaved intents — run 'intent save --topology' first, or 'intent reload --topology' to discard")
	}
	na.closeNode()
	node, err := na.net.InitFromDeviceIntent(ctx, na.device)
	if err != nil {
		return err
	}
	na.node = node
	log.Printf("newtron-server: initialized %s from device intents", na.device)
	return nil
}

// ensureTopologyIntent ensures the cached node was built from topology.json
// steps. If the current node is actuated, it is destroyed and replaced with
// a topology-sourced node. If the node is already topology-sourced but has
// transport connected (left over from a previous Drift/Reconcile), transport
// is disconnected to prevent CRUD from leaking to Redis.
//
// Architecture §3: "Actuated online → topology offline: ensureTopologyIntent
// closes the online node, creates a new one from topology.json."
func (na *NodeActor) ensureTopologyIntent() error {
	if na.node != nil && !na.node.HasActuatedIntent() {
		// Already topology-sourced. Disconnect transport if present so
		// CRUD operations don't leak to Redis through a leftover connection.
		na.node.DisconnectTransport()
		return nil
	}
	na.closeNode()
	node, err := na.net.BuildTopologyNode(na.device)
	if err != nil {
		return err
	}
	na.node = node
	log.Printf("newtron-server: built %s from topology", na.device)
	return nil
}

// ensureLoopbackIntent ensures the cached node is suitable for
// loopback mode: present, topology-sourced (actuatedIntent=false),
// transport disconnected.
//
// On a topology-sourced cached node, reuses so CLI mutations accumulate
// across requests (the offline-testing property). On an actuated
// cached node, destroys and rebuilds — actuated nodes carry
// connection/lock semantics that loopback's precondition gate rejects.
// Symmetric with ensureTopologyIntent which makes the same choice
// for the opposite direction.
//
// The rebuilt node has conn=nil and actuatedIntent=false. All
// operations run against the projection: Lock/Apply/Verify/Save are
// no-ops, intents accumulate in memory, RebuildProjection replays
// from in-memory intents.
func (na *NodeActor) ensureLoopbackIntent() error {
	if na.node != nil && !na.node.HasActuatedIntent() {
		na.node.DisconnectTransport()
		return nil
	}
	na.closeNode()
	node, err := na.net.BuildTopologyNode(na.device)
	if err != nil {
		return err
	}
	na.node = node
	log.Printf("newtron-server: built %s in loopback mode (offline config testing)", na.device)
	return nil
}

// execute is the unified entry point for all operations. It reads mode from
// the request context (injected by withMode middleware), ensures the node is
// in the correct state, then rebuilds the projection from fresh intents.
//
// All operations — reads and writes — see authoritative state after execute().
// Writes acquire their own lock via Execute(); reads
// don't need a lock.
//
// Architecture §10: "execute — single entry point. ONE branch for mode
// resolution."
// Architecture §1: "Intent DB is primary state."
// CLAUDE.md: "In actuated mode, the device's own NEWTRON_INTENT records
// ARE the authoritative state."
func (na *NodeActor) execute(ctx context.Context, fn func() (any, error)) (any, error) {
	return na.do(ctx, func() (any, error) {
		mode := modeFromCtx(ctx)
		switch mode {
		case ModeTopology:
			if err := na.ensureTopologyIntent(); err != nil {
				return nil, err
			}
		case ModeLoopback:
			if err := na.ensureLoopbackIntent(); err != nil {
				return nil, err
			}
		default:
			if err := na.ensureActuatedIntent(ctx); err != nil {
				return nil, err
			}
		}
		// Re-read intents from device (when connected) and rebuild projection.
		// All operations — reads and writes — see fresh, authoritative state.
		if err := na.node.RebuildProjection(ctx); err != nil {
			na.closeNode()
			return nil, err
		}
		result, err := fn()
		if err != nil {
			return nil, err
		}
		// ?persist=topology hook (#75C). Data-driven: the persist work
		// only runs when fn actually dirtied the intent tree, so read-only
		// handlers are no-ops and /intent/save (which clears the flag at
		// the end of its own closure) doesn't double-write.
		if persistFromCtx(ctx) == PersistTopology && na.node != nil && na.node.HasUnsavedIntents() {
			if err := na.saveTopologyNow(); err != nil {
				// The in-memory write (and the device write in intent mode)
				// already succeeded; only the topology.json persist failed.
				// Phrase it mode-agnostically — in topology mode the device
				// is never touched, so "device updated" would mislead.
				return nil, fmt.Errorf("write succeeded but topology.json persist failed: %w", err)
			}
		}
		return result, nil
	})
}

// saveTopologyNow rewrites this device's entry in topology.json from the
// current intent tree, then clears the unsaved flag. Mirrors the verb in
// Network.SaveDeviceIntents (§13 — same concept, same name). Used by
// (1) the ?persist=topology hook above and (2) handleSave, so the two
// share one body. Must be called on the actor goroutine so na.node
// access is race-free.
func (na *NodeActor) saveTopologyNow() error {
	tree := na.node.Tree()
	steps := make([]spec.TopologyStep, len(tree.Steps))
	for i, s := range tree.Steps {
		steps[i] = spec.TopologyStep{URL: s.URL, Params: s.Params}
	}
	if err := na.net.SaveDeviceIntents(na.device, steps); err != nil {
		return err
	}
	na.node.ClearUnsavedIntents()
	return nil
}

// ============================================================================
// Node operation helpers — compositions on execute
// ============================================================================

// connectAndRead ensures the correct node mode, checks connectivity via Ping,
// and runs a read-only function. Ping is a no-op without transport (topology
// offline mode — architecture §7 transport guard).
func (na *NodeActor) connectAndRead(ctx context.Context, fn func(n *newtron.Node) (any, error)) (any, error) {
	return na.execute(ctx, func() (any, error) {
		if err := na.node.Ping(ctx); err != nil {
			// Ping failure means the SSH tunnel is dead.
			na.closeNode()
			return nil, err
		}
		return fn(na.node)
	})
}

// connectAndExecute ensures the correct node mode and runs fn inside Execute
// (lock → fn → commit → save → unlock). In topology offline mode, Lock/Apply/
// Unlock are no-ops — intents accumulate in the projection without delivery.
//
// Dry-run and error paths restore the intent DB; the projection is rebuilt
// by execute() at the start of the next operation.
func (na *NodeActor) connectAndExecute(
	ctx context.Context,
	opts newtron.ExecOpts,
	fn func(ctx context.Context, n *newtron.Node) error,
) (any, error) {
	return na.execute(ctx, func() (any, error) {
		return na.node.Execute(ctx, opts, func(ctx context.Context) error {
			return fn(ctx, na.node)
		})
	})
}

// resetTimer safely resets a timer, draining the channel if it already fired.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
