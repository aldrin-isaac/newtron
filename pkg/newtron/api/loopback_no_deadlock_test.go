package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// loopbackPortResolver makes an HTTP call back into the same newtron-server
// during SSHPort resolution. This is the reentrancy shape that triggered
// the original cycle-deadlock from issue #97 — newtron handler → resolver
// (HTTP out) → newtlab handler → newtron handler (HTTP in). The test wires
// this shape directly and asserts the inner call completes within a
// deadline. With PR C stripping the API-layer lock, the cycle is
// structurally impossible: handlers hold no lock across outbound HTTP,
// so no inner request can queue behind an outer one.
type loopbackPortResolver struct {
	targetURL string
}

func (r *loopbackPortResolver) SSHPort(ctx context.Context, topology, device string) (int, error) {
	if r.targetURL == "" {
		// Not yet wired up — test setup orders matters; return a placeholder.
		return 22, nil
	}
	url := r.targetURL + "/newtron/v1/networks/default/topology"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("loopback GET /topology: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("loopback GET /topology: %d", resp.StatusCode)
	}
	return 13000, nil
}

// TestAPI_LoopbackHTTPDoesNotDeadlock pins the contract that PR C
// secures structurally: a handler that triggers an outbound HTTP call
// back into the same server must complete within a reasonable deadline.
//
// Pre-PR-C, the API layer wrapped every handler in ne.read()/ne.write()
// — sync.RWMutex closure wrappers. A read holding RLock during an
// outbound HTTP call would block a concurrent writer's Lock attempt;
// Go's RWMutex writer-preference would then queue any inner reader
// behind the writer; the inner reader couldn't complete until the
// outer reader finished; the outer reader was waiting on the HTTP
// response. Deadlock until the http.Client timeout fired.
//
// Post-PR-C, the API layer holds no spec lock at all. The engine layer
// holds keyNetworkSpec/keyTopology RWMutexes inside each public Network
// method, but those locks are taken-and-released around in-memory
// operations only — never across HTTP. So the cycle structurally cannot
// form.
//
// Uses the 2node-vs topology (the smallest spec dir with a host device).
// If anyone reintroduces a lock-held-across-loopback in the API layer,
// this test fails on the 5-second deadline.
func TestAPI_LoopbackHTTPDoesNotDeadlock(t *testing.T) {
	resolver := &loopbackPortResolver{}
	s := NewServer(Config{PortResolver: resolver})
	specDir := filepath.Join(repoRoot(t), "newtrun", "topologies", "2node-vs", "specs")
	if err := s.RegisterNetwork("default", specDir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	ts := httptest.NewServer(s.HTTPServer().Handler)
	t.Cleanup(ts.Close)
	resolver.targetURL = ts.URL

	done := make(chan error, 1)
	go func() {
		url := ts.URL + "/newtron/v1/networks/default/hosts/host1"
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			done <- err
			return
		}
		client := &http.Client{Timeout: 4 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			done <- fmt.Errorf("/hosts/host1: status %d", resp.StatusCode)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loopback chain completed with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loopback chain did not complete in 5s — possible API-layer lock regression " +
			"(a handler is holding a lock across an outbound HTTP call)")
	}
}
