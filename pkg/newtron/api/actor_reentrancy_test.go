package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestNetworkActor_LoopbackHTTPDoesNotDeadlock pins the contract from
// issue #97: an HTTP call made from inside a NetworkActor closure to any
// read-only spec endpoint must complete promptly. Before PR #96 the inner
// call queued on the same actor goroutine that was running the outer
// closure; the inner http.Client timed out at 30s. Re-introducing actor
// wrappers on these handlers would flip this test from a millisecond
// completion to a deadlock.
//
// The test does not depend on the in-process spec client wiring in
// cmd/newt-server — it asserts the engine-level contract: read-only
// endpoints stay outside the actor regardless of how callers reach them
// (HTTP, in-process, future transports).
func TestNetworkActor_LoopbackHTTPDoesNotDeadlock(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.HTTPServer().Handler)
	t.Cleanup(ts.Close)

	na := s.getNetwork("default")
	if na == nil {
		t.Fatal("default network not registered")
	}

	// Endpoints newtlab.NewLab calls. If any one of these is wrapped
	// in na.do(), the loopback call below stalls on the actor channel.
	endpoints := []string{
		"/newtron/v1/network/default/topology",
		"/newtron/v1/network/default/platform",
		"/newtron/v1/network/default/profile/switch1",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				_, err := na.do(context.Background(), func() (any, error) {
					client := &http.Client{Timeout: 5 * time.Second}
					resp, err := client.Get(ts.URL + ep)
					if err != nil {
						return nil, err
					}
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						return nil, &httpStatusError{code: resp.StatusCode, body: ep}
					}
					return nil, nil
				})
				done <- err
			}()

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("loopback %s failed: %v", ep, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("deadlock: actor closure calling %s did not return in 2s "+
					"(handler must stay outside NetworkActor.do)", ep)
			}
		})
	}
}

type httpStatusError struct {
	code int
	body string
}

func (e *httpStatusError) Error() string { return e.body }
