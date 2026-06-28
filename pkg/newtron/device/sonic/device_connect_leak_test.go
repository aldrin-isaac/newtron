// Tests for the partial-init resource cleanup in Connect (issue #83).
// Connect allocates resources (SSH tunnel goroutines, Redis clients)
// incrementally. If a fatal step fails partway, the deferred cleanup
// must release whatever was allocated — otherwise the tunnel goroutine
// and its localhost listener leak across every failed Connect call.
package sonic

import (
	"context"
	"net"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestClosePartial_NilFields verifies closePartial is safe to call on a
// freshly-constructed Device that never started Connect. Each field
// guard (`if d.x != nil`) protects the Close call.
func TestClosePartial_NilFields(t *testing.T) {
	d := NewDevice("switch1", &spec.ResolvedNodeSpec{})
	// Must not panic; all fields stay nil.
	d.closePartial()
	if d.client != nil || d.stateClient != nil || d.applClient != nil || d.asicClient != nil || d.tunnel != nil {
		t.Error("closePartial on a fresh Device should leave all fields nil")
	}
}

// TestClosePartial_NilsAllocatedFields constructs a Device with the
// non-SSH fields populated against a closed-target Redis client and
// verifies closePartial calls Close + sets them to nil. Pinning the
// nil-out is the contract Disconnect depends on — if closePartial
// returned with d.client still set, a follow-up Disconnect (gated on
// d.connected, which is false here) would skip cleanup AND a future
// Connect on the same Device would leak the prior client when it
// re-assigns d.client.
func TestClosePartial_NilsAllocatedFields(t *testing.T) {
	// A bound-but-immediately-closed listener gives us a TCP target
	// that resolves to a real port number without holding the port.
	// The ConfigDBClient just needs a syntactically-valid addr to
	// construct — go-redis is lazy, so Connect-failure isn't required
	// for this test.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	d := NewDevice("switch1", &spec.ResolvedNodeSpec{})
	d.client = NewConfigDBClient(addr)
	d.stateClient = NewStateDBClient(addr)
	d.applClient = NewAppDBClient(addr)
	d.asicClient = NewAsicDBClient(addr)
	// d.tunnel intentionally left nil — SSHTunnel requires a real SSH
	// server to construct; the nil-guard path is what matters here.

	d.closePartial()

	if d.client != nil {
		t.Error("d.client: closePartial did not nil")
	}
	if d.stateClient != nil {
		t.Error("d.stateClient: closePartial did not nil")
	}
	if d.applClient != nil {
		t.Error("d.applClient: closePartial did not nil")
	}
	if d.asicClient != nil {
		t.Error("d.asicClient: closePartial did not nil")
	}
}

// TestConnect_PartialFailureNilsClient drives Connect against an
// unreachable Redis. The ConfigDB connect step fails, the deferred
// cleanup runs, and the client must be released before Connect
// returns. Otherwise a caller that retries Connect on the same Device
// leaks the failed-attempt client.
//
// The Profile uses MgmtIP "127.0.0.1" with no SSHUser/SSHPass — Connect
// takes the direct Redis path to 127.0.0.1:6379. The test assumes
// nothing is listening there in the CI/dev environment (true for the
// project's lab hosts); a stray local Redis would mask the failure.
func TestConnect_PartialFailureNilsClient(t *testing.T) {
	d := NewDevice("switch1", &spec.ResolvedNodeSpec{MgmtIP: "127.0.0.1"})

	err := d.Connect(context.Background())
	if err == nil {
		t.Fatal("expected Connect to fail (no Redis at 127.0.0.1:6379)")
	}

	// The fix: d.client is nil after a failed Connect, having been
	// closed and released by closePartial. Pre-fix behavior: d.client
	// would point to a closed go-redis client, leaking the struct and
	// (when SSH is involved) the tunnel goroutine.
	if d.client != nil {
		t.Error("d.client should be nil after failed Connect (resource leak — see issue #83)")
	}
	if d.connected {
		t.Error("d.connected should be false after failed Connect")
	}
}
