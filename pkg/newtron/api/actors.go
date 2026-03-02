package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
)

// ============================================================================
// Actor message types
// ============================================================================

// request is a message sent to an actor's channel.
type request struct {
	ctx    context.Context
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
	net     *newtron.Network
	specDir string

	// nodeActors maps device names to their NodeActors.
	mu         sync.Mutex
	nodeActors map[string]*NodeActor

	requests chan request
	done     chan struct{}
}

// newNetworkActor creates and starts a NetworkActor.
func newNetworkActor(net *newtron.Network, specDir string) *NetworkActor {
	na := &NetworkActor{
		net:        net,
		specDir:    specDir,
		nodeActors: make(map[string]*NodeActor),
		requests:   make(chan request, 64),
		done:       make(chan struct{}),
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
	case na.requests <- request{ctx: ctx, fn: fn, result: res}:
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
	actor := newNodeActor(na.net, device)
	na.nodeActors[device] = actor
	return actor
}

// activeNodeCount returns the number of active NodeActors.
func (na *NetworkActor) activeNodeCount() int {
	na.mu.Lock()
	defer na.mu.Unlock()
	return len(na.nodeActors)
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

// NodeActor serializes all operations on a single device.
// Each request does connect → execute → disconnect (stateless).
type NodeActor struct {
	net    *newtron.Network
	device string

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
func newNodeActor(net *newtron.Network, device string) *NodeActor {
	actor := &NodeActor{
		net:        net,
		device:     device,
		composites: make(map[string]*compositeEntry),
		requests:   make(chan request, 64),
		done:       make(chan struct{}),
	}
	go actor.run()
	return actor
}

// run is the actor's event loop.
func (na *NodeActor) run() {
	defer close(na.done)
	for req := range na.requests {
		val, err := req.fn()
		req.result <- response{value: val, err: err}
	}
}

// do sends a closure to the actor and waits for the result.
func (na *NodeActor) do(ctx context.Context, fn func() (any, error)) (any, error) {
	res := make(chan response, 1)
	select {
	case na.requests <- request{ctx: ctx, fn: fn, result: res}:
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

// connectAndRead connects to the device, runs a read-only function, then disconnects.
func (na *NodeActor) connectAndRead(ctx context.Context, fn func(n *newtron.Node) (any, error)) (any, error) {
	return na.do(ctx, func() (any, error) {
		node, err := na.net.Connect(ctx, na.device)
		if err != nil {
			return nil, err
		}
		defer node.Close()
		return fn(node)
	})
}

// connectAndExecute connects, runs fn inside Execute (lock→fn→commit→save→unlock), then disconnects.
func (na *NodeActor) connectAndExecute(
	ctx context.Context,
	opts newtron.ExecOpts,
	fn func(ctx context.Context, n *newtron.Node) error,
) (any, error) {
	return na.do(ctx, func() (any, error) {
		node, err := na.net.Connect(ctx, na.device)
		if err != nil {
			return nil, err
		}
		defer node.Close()
		return node.Execute(ctx, opts, func(ctx context.Context) error {
			return fn(ctx, node)
		})
	})
}
