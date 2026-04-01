package api

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
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
// NetworkActor — serializes spec operations for a single Network
// ============================================================================

// NetworkActor owns a *newtron.Network and serializes all operations on it.
type NetworkActor struct {
	net         *newtron.Network
	idleTimeout time.Duration

	// nodeActors maps device names to their NodeActors.
	mu         sync.Mutex
	nodeActors map[string]*NodeActor

	requests chan request
	done     chan struct{}
}

// newNetworkActor creates and starts a NetworkActor.
func newNetworkActor(net *newtron.Network, idleTimeout time.Duration) *NetworkActor {
	na := &NetworkActor{
		net:         net,
		idleTimeout: idleTimeout,
		nodeActors:  make(map[string]*NodeActor),
		requests:    make(chan request, 64),
		done:        make(chan struct{}),
	}
	go na.run()
	return na
}

// run is the actor's event loop.
func (na *NetworkActor) run() {
	defer close(na.done)
	for req := range na.requests {
		val, err := req.fn()
		req.result <- response{value: val, err: err}
	}
}

// do sends a closure to the actor and waits for the result.
func (na *NetworkActor) do(ctx context.Context, fn func() (any, error)) (any, error) {
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

// getNodeActor returns or creates a NodeActor for the given device.
func (na *NetworkActor) getNodeActor(device string) *NodeActor {
	na.mu.Lock()
	defer na.mu.Unlock()
	if actor, ok := na.nodeActors[device]; ok {
		return actor
	}
	actor := newNodeActor(na.net, device, na.idleTimeout)
	na.nodeActors[device] = actor
	return actor
}

// stop shuts down the NetworkActor and all its NodeActors.
func (na *NetworkActor) stop() {
	na.mu.Lock()
	for _, nodeActor := range na.nodeActors {
		nodeActor.stop()
	}
	na.nodeActors = make(map[string]*NodeActor)
	na.mu.Unlock()

	close(na.requests)
	<-na.done
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
		if mode == ModeTopology {
			if err := na.ensureTopologyIntent(); err != nil {
				return nil, err
			}
		} else {
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
		return fn()
	})
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
