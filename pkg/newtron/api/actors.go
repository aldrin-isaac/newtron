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

// NodeActor serializes all operations on a single device. It caches the SSH
// connection (via *newtron.Node) between requests and closes it after an idle
// timeout. Each request still locks/unlocks and refreshes CONFIG_DB — only the
// SSH tunnel is reused.
type NodeActor struct {
	net         *newtron.Network
	device      string
	idleTimeout time.Duration

	// Cached device connection. Nil when not connected.
	// Only accessed from the actor goroutine (run loop).
	node *newtron.Node

	// composites stores generated CompositeInfo by UUID with expiry.
	compositeMu sync.Mutex
	composites  map[string]*compositeEntry

	requests chan request
	done     chan struct{}
}

// compositeEntry holds a composite handle with expiry.
type compositeEntry struct {
	info      *newtron.CompositeInfo
	expiresAt time.Time
}

// compositeExpiry is the TTL for stored composites.
const compositeExpiry = 10 * time.Minute

// newNodeActor creates and starts a NodeActor.
func newNodeActor(net *newtron.Network, device string, idleTimeout time.Duration) *NodeActor {
	actor := &NodeActor{
		net:         net,
		device:      device,
		idleTimeout: idleTimeout,
		composites:  make(map[string]*compositeEntry),
		requests:    make(chan request, 64),
		done:        make(chan struct{}),
	}
	go actor.run()
	return actor
}

// run is the actor's event loop. It processes requests and closes the cached
// SSH connection after idleTimeout of inactivity.
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
			// Reset idle timer if we have a cached connection.
			if na.node != nil {
				resetTimer(idle, na.idleTimeout)
			}
		case <-idle.C:
			log.Printf("newtron-server: closing idle connection to %s", na.device)
			na.closeNode()
		}
	}
}

// getNode returns the cached SSH connection or establishes a new one.
// Must only be called from within the actor goroutine (via do).
func (na *NodeActor) getNode(ctx context.Context) (*newtron.Node, error) {
	if na.node != nil {
		return na.node, nil
	}
	node, err := na.net.Connect(ctx, na.device)
	if err != nil {
		return nil, err
	}
	na.node = node
	log.Printf("newtron-server: connected to %s (idle timeout: %s)", na.device, na.idleTimeout)
	return node, nil
}

// closeNode closes and discards the cached connection.
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

// storeComposite saves a CompositeInfo with a UUID key and returns the UUID.
func (na *NodeActor) storeComposite(id string, ci *newtron.CompositeInfo) {
	na.compositeMu.Lock()
	defer na.compositeMu.Unlock()

	// Evict expired entries opportunistically
	now := time.Now()
	for k, v := range na.composites {
		if now.After(v.expiresAt) {
			delete(na.composites, k)
		}
	}

	na.composites[id] = &compositeEntry{
		info:      ci,
		expiresAt: now.Add(compositeExpiry),
	}
}

// getComposite retrieves a stored CompositeInfo by UUID.
func (na *NodeActor) getComposite(id string) (*newtron.CompositeInfo, error) {
	na.compositeMu.Lock()
	defer na.compositeMu.Unlock()

	entry, ok := na.composites[id]
	if !ok {
		return nil, fmt.Errorf("composite handle '%s' not found or expired", id)
	}
	if time.Now().After(entry.expiresAt) {
		delete(na.composites, id)
		return nil, fmt.Errorf("composite handle '%s' has expired", id)
	}
	return entry.info, nil
}

// removeComposite deletes a stored CompositeInfo by UUID.
func (na *NodeActor) removeComposite(id string) {
	na.compositeMu.Lock()
	defer na.compositeMu.Unlock()
	delete(na.composites, id)
}

// stop shuts down the NodeActor.
func (na *NodeActor) stop() {
	close(na.requests)
	<-na.done
}

// ============================================================================
// Node operation helpers
// ============================================================================

// connectAndRead gets a connection, refreshes CONFIG_DB from Redis, and runs
// a read-only function. Refresh ensures reads always see current device state.
func (na *NodeActor) connectAndRead(ctx context.Context, fn func(n *newtron.Node) (any, error)) (any, error) {
	return na.do(ctx, func() (any, error) {
		node, err := na.getNode(ctx)
		if err != nil {
			return nil, err
		}
		if err := node.Refresh(ctx); err != nil {
			// Refresh failure means the SSH tunnel is likely dead.
			na.closeNode()
			return nil, err
		}
		return fn(node)
	})
}

// connectAndLocked gets a connection, locks (Lock refreshes CONFIG_DB), runs
// fn, then unlocks. Use for operations that write directly to Redis (e.g.
// DeliverComposite) rather than going through the ChangeSet/Commit model.
func (na *NodeActor) connectAndLocked(ctx context.Context, fn func(n *newtron.Node) (any, error)) (any, error) {
	return na.do(ctx, func() (any, error) {
		node, err := na.getNode(ctx)
		if err != nil {
			return nil, err
		}
		if err := node.Lock(); err != nil {
			// Lock failure means Redis/SSH is unreachable.
			na.closeNode()
			return nil, err
		}
		defer node.Unlock()
		return fn(node)
	})
}

// connectAndExecute gets a connection and runs fn inside Execute
// (lock → fn → commit → save → unlock). Lock refreshes CONFIG_DB, so writes
// always operate on current device state.
func (na *NodeActor) connectAndExecute(
	ctx context.Context,
	opts newtron.ExecOpts,
	fn func(ctx context.Context, n *newtron.Node) error,
) (any, error) {
	return na.do(ctx, func() (any, error) {
		node, err := na.getNode(ctx)
		if err != nil {
			return nil, err
		}
		val, err := node.Execute(ctx, opts, func(ctx context.Context) error {
			return fn(ctx, node)
		})
		if err != nil {
			// Clear any partial pending changesets so the next request
			// starts clean. Close the connection if the error looks like
			// a transport failure (Lock/Commit/Save use SSH+Redis).
			node.Rollback()
		}
		return val, err
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
