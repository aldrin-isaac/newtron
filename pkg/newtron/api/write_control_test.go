package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// ---- store logic (the reservation state machine) ----

func TestWriteControl_StoreLifecycle(t *testing.T) {
	ne := &networkEntity{}

	// Free → enforce refuses (default-closed).
	if err := ne.enforceWrite("n", "alice"); err == nil {
		t.Fatal("free network: enforceWrite should refuse (no holder), got nil")
	}

	// alice requests → granted; her writes pass, bob's don't.
	if _, prior, err := ne.requestControl("n", "alice", false); err != nil || prior != "" {
		t.Fatalf("alice request: err=%v prior=%q, want grant", err, prior)
	}
	if err := ne.enforceWrite("n", "alice"); err != nil {
		t.Errorf("holder write refused: %v", err)
	}
	var wce *newtron.WriteControlError
	err := ne.enforceWrite("n", "bob")
	if !asWriteControl(err, &wce) || wce.Holder != "alice" {
		t.Errorf("bob write: err=%v, want WriteControlError holder=alice", err)
	}

	// bob requests without force → 409 naming alice.
	if _, _, err := ne.requestControl("n", "bob", false); !asWriteControl(err, &wce) || wce.Holder != "alice" {
		t.Errorf("bob request(no force): err=%v, want held-by-alice", err)
	}

	// bob force-takes over → granted, prior=alice; now alice is locked out.
	wc, prior, err := ne.requestControl("n", "bob", true)
	if err != nil || prior != "alice" || wc.Holder != "bob" {
		t.Fatalf("bob takeover: wc=%+v prior=%q err=%v, want holder=bob prior=alice", wc, prior, err)
	}
	if err := ne.enforceWrite("n", "alice"); !asWriteControl(err, &wce) || wce.Holder != "bob" {
		t.Errorf("displaced alice write: err=%v, want held-by-bob", err)
	}

	// relinquish by non-holder is a no-op; by holder frees it.
	ne.relinquishControl("alice")
	if _, held := ne.controlStatus(); !held {
		t.Error("non-holder relinquish should be a no-op")
	}
	ne.relinquishControl("bob")
	if _, held := ne.controlStatus(); held {
		t.Error("holder relinquish should free control")
	}
	if err := ne.enforceWrite("n", "carol"); err == nil {
		t.Error("after relinquish: enforceWrite should refuse again (free)")
	}
}

func asWriteControl(err error, target **newtron.WriteControlError) bool {
	wce, ok := err.(*newtron.WriteControlError)
	if ok {
		*target = wce
	}
	return ok
}

// ---- HTTP enforcement (middleware + handlers, flag on) ----

func newWriteControlServer(t *testing.T) *Server {
	t.Helper()
	// Copy the fixture to a temp dir — the test's holder-write persists a
	// service, which must not touch the committed network.json.
	src := filepath.Join(repoRoot(t), "networks", "1node-vs")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	s := NewServer(Config{EnforceWriteControl: true, AuditCallerHeader: "X-Newtron-Caller"})
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(httptest.NewRequest("", "/", nil).Context()) })
	return s
}

// do sends a request as caller (empty = no identity header) and returns the recorder.
func wcDo(t *testing.T, s *Server, method, path, caller, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if caller != "" {
		r.Header.Set("X-Newtron-Caller", caller)
	}
	w := httptest.NewRecorder()
	s.HTTPServer().Handler.ServeHTTP(w, r)
	return w
}

func TestWriteControl_HTTPEnforcement(t *testing.T) {
	s := newWriteControlServer(t)
	const create = "/newtron/v1/networks/default/create-service"
	body := `{"name":"wc-test","service_type":"routed"}`

	// 1. A write with nobody holding control → 409 (default-closed).
	if w := wcDo(t, s, "POST", create, "alice", body); w.Code != http.StatusConflict {
		t.Fatalf("write without holder: status=%d, want 409; body=%s", w.Code, w.Body.String())
	}

	// 2. alice claims control.
	if w := wcDo(t, s, "POST", "/newtron/v1/networks/default/control/request", "alice", `{}`); w.Code != http.StatusOK {
		t.Fatalf("alice claim: status=%d, want 200; body=%s", w.Code, w.Body.String())
	}

	// 3. bob's write is refused with a WriteControlError naming alice.
	w := wcDo(t, s, "POST", create, "bob", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("bob write: status=%d, want 409", w.Code)
	}
	var env httputil.APIResponse
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	raw, _ := json.Marshal(env.Data)
	var got newtron.WriteControlError
	_ = json.Unmarshal(raw, &got)
	if got.Holder != "alice" || got.Network != "default" {
		t.Errorf("bob write payload = %+v, want holder=alice network=default", got)
	}

	// 4. alice's own write goes through (she holds control).
	if w := wcDo(t, s, "POST", create, "alice", body); w.Code != http.StatusCreated {
		t.Fatalf("alice write: status=%d, want 201; body=%s", w.Code, w.Body.String())
	}

	// 5. Exemptions: reads, dry-runs, and the control endpoints don't need control.
	if w := wcDo(t, s, "GET", "/newtron/v1/networks/default/services", "bob", ""); w.Code != http.StatusOK {
		t.Errorf("read by non-holder: status=%d, want 200", w.Code)
	}
	if w := wcDo(t, s, "POST", create+"?dry_run=true", "bob", `{"name":"dry","service_type":"routed"}`); w.Code == http.StatusConflict {
		t.Error("dry-run by non-holder was blocked by write control; should be exempt")
	}
	if w := wcDo(t, s, "GET", "/newtron/v1/networks/default/control", "bob", ""); w.Code != http.StatusOK {
		t.Errorf("control status read: status=%d, want 200", w.Code)
	}

	// 6. alice relinquishes → writes refused again.
	if w := wcDo(t, s, "POST", "/newtron/v1/networks/default/control/relinquish", "alice", ``); w.Code != http.StatusOK {
		t.Fatalf("relinquish: status=%d, want 200", w.Code)
	}
	if w := wcDo(t, s, "POST", create, "alice", body); w.Code != http.StatusConflict {
		t.Errorf("write after relinquish: status=%d, want 409 (free → default-closed)", w.Code)
	}
}
