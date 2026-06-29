package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// TestServiceDetailReturnsRouting — a routed service's routing block (protocol,
// peer_as, policies, redistribute) is accepted on create AND returned on the
// service read (ai-instructions §24: a write-accepted field must be readable).
// Previously routing was write-only, which silently masked dropped peer_as.
func TestServiceDetailReturnsRouting(t *testing.T) {
	src := filepath.Join(repoRoot(t), "networks", "1node-vs")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	s := NewServer(Config{})
	if err := s.RegisterNetwork("default", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop(httptest.NewRequest("", "/", nil).Context()) })

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		w := httptest.NewRecorder()
		s.HTTPServer().Handler.ServeHTTP(w, r)
		return w
	}
	detail := func(name string) newtron.ServiceDetail {
		w := do("GET", "/newtron/v1/networks/default/services/"+name, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", name, w.Code, w.Body.String())
		}
		var env struct {
			Data newtron.ServiceDetail `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		return env.Data
	}

	const base = "/newtron/v1/networks/default"

	// Routed service WITH a routing block.
	body := `{"name":"RTEST","service_type":"routed","routing":{"protocol":"bgp","peer_as":"65010","redistribute":true}}`
	if w := do("POST", base+"/create-service", body); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create routed: status=%d body=%s", w.Code, w.Body.String())
	}
	d := detail("RTEST")
	if d.Routing == nil {
		t.Fatal("routing block omitted from service read (the §24 gap) — want it returned")
	}
	if d.Routing.Protocol != "bgp" || d.Routing.PeerAS != "65010" {
		t.Errorf("routing round-trip: got %+v, want protocol=bgp peer_as=65010", d.Routing)
	}
	if d.Routing.Redistribute == nil || *d.Routing.Redistribute != true {
		t.Errorf("redistribute not round-tripped: got %v", d.Routing.Redistribute)
	}

	// A service without routing omits the block (omitempty) — not a spurious {}.
	bridged := `{"name":"BTEST","service_type":"routed"}`
	if w := do("POST", base+"/create-service", bridged); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create no-routing: status=%d body=%s", w.Code, w.Body.String())
	}
	if d := detail("BTEST"); d.Routing != nil {
		t.Errorf("service with no routing: got routing=%+v, want nil", d.Routing)
	}
}
