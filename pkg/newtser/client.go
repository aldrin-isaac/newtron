package newtser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"
)

// Registration is a backend's handle for an active newtser registration.
// Backends create one in main.go and Close() it on graceful shutdown to
// deregister. The keepalive goroutine runs heartbeats until Close().
//
// On registration failure (newtser unreachable, 4xx response, etc.),
// the keepalive retries with exponential backoff up to KeepaliveMax.
// A backend that cannot register continues to serve direct traffic on
// its loopback port — registration is opt-in via the --newtser flag,
// not a hard prerequisite.
type Registration struct {
	URL      string // newtser base URL
	Name     string
	Version  string
	Upstream string
	Logger   *log.Logger

	HeartbeatInterval time.Duration // default 30s
	KeepaliveMax      time.Duration // max backoff between failed re-register attempts; default 30s

	cancel context.CancelFunc
	done   chan struct{}
}

// Register performs the initial registration and starts the keepalive
// goroutine. Returns the *Registration handle (use Close to stop).
// On initial-registration failure, Register still returns a handle —
// the keepalive will retry — but logs the failure.
func Register(ctx context.Context, opts Registration) *Registration {
	if opts.HeartbeatInterval == 0 {
		opts.HeartbeatInterval = 30 * time.Second
	}
	if opts.KeepaliveMax == 0 {
		opts.KeepaliveMax = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}

	loopCtx, cancel := context.WithCancel(ctx)
	r := &opts
	r.cancel = cancel
	r.done = make(chan struct{})

	go r.keepalive(loopCtx)
	return r
}

// Close stops the keepalive goroutine and sends a best-effort
// deregister. Called by backends on graceful shutdown.
func (r *Registration) Close() {
	r.cancel()
	<-r.done

	// Best-effort deregister with a short timeout — we're shutting
	// down, and the eviction loop will clean up if this fails.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.deregister(ctx); err != nil {
		r.Logger.Printf("newtser deregister %q failed: %v (registration will expire via eviction)", r.Name, err)
	}
}

// keepalive registers (with retry on failure) then heartbeats at the
// configured interval. Re-registration is also the response to a 404
// from heartbeat (newtser evicted us; rejoin).
func (r *Registration) keepalive(ctx context.Context) {
	defer close(r.done)

	r.registerWithBackoff(ctx)

	ticker := time.NewTicker(r.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.heartbeat(ctx); err != nil {
				if isNotRegistered(err) {
					r.Logger.Printf("newtser: heartbeat for %q said 404; re-registering", r.Name)
					r.registerWithBackoff(ctx)
				} else {
					r.Logger.Printf("newtser: heartbeat for %q failed: %v", r.Name, err)
				}
			}
		}
	}
}

// registerWithBackoff retries POST /services until success or ctx
// cancellation. Backoff doubles each attempt (1s, 2s, 4s, ...) up to
// KeepaliveMax.
func (r *Registration) registerWithBackoff(ctx context.Context) {
	backoff := time.Second
	for attempt := 0; ; attempt++ {
		err := r.register(ctx)
		if err == nil {
			r.Logger.Printf("newtser: registered %q → %s", r.Name, r.Upstream)
			return
		}
		if ctx.Err() != nil {
			return
		}
		r.Logger.Printf("newtser: register %q failed (attempt %d): %v; retrying in %s", r.Name, attempt+1, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = time.Duration(math.Min(float64(backoff*2), float64(r.KeepaliveMax)))
	}
}

func (r *Registration) register(ctx context.Context) error {
	body, _ := json.Marshal(RegisterRequest{
		Name:     r.Name,
		Version:  r.Version,
		Upstream: r.Upstream,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+"/newtser/v1/services", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("register: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *Registration) heartbeat(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.URL+"/newtser/v1/services/"+r.Name+"/heartbeat", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotRegistered
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("heartbeat: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *Registration) deregister(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		r.URL+"/newtser/v1/services/"+r.Name, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("deregister: HTTP %d", resp.StatusCode)
	}
	return nil
}

// errNotRegistered is the sentinel returned by heartbeat() when
// newtser reports 404 — the registration has been evicted, the
// keepalive loop must rejoin via Register.
var errNotRegistered = fmt.Errorf("newtser: registration not found")

func isNotRegistered(err error) bool { return err == errNotRegistered }
