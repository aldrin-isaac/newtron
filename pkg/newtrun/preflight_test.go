package newtrun

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestPreflightInterfaces verifies the #403 pre-flight: a topology interface
// that isn't in its node's platform inventory fails at suite load, before
// deploy, and a valid topology passes. The inventory authority is the same
// GET /nodes/{node}/interfaces newtlab and newtron enforce.
func TestPreflightInterfaces(t *testing.T) {
	inv := map[string][]newtron.InterfaceInventoryEntry{
		"switch1": {{Name: "Ethernet0"}, {Name: "Ethernet4"}},
		"host1":   {{Name: "eth0"}, {Name: "eth1"}},
	}
	topo := &spec.TopologySpecFile{
		Links: []*spec.TopologyLink{
			{A: "switch1:Ethernet0", Z: "host1:eth0"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		switch {
		case strings.HasSuffix(req.URL.Path, "/topology"):
			_ = enc.Encode(map[string]any{"data": topo})
		case strings.HasSuffix(req.URL.Path, "/interfaces"):
			parts := strings.Split(req.URL.Path, "/")
			device := parts[len(parts)-2]
			_ = enc.Encode(map[string]any{"data": inv[device]})
		default:
			_ = enc.Encode(map[string]any{"data": map[string]any{}})
		}
	}))
	defer server.Close()

	r := &Runner{Client: client.New(server.URL, "default")}

	// All endpoints are in inventory → passes.
	if err := r.preflightInterfaces(); err != nil {
		t.Fatalf("valid topology should pass pre-flight: %v", err)
	}

	// A host interface outside the inventory → fails, naming the offender.
	topo.Links = append(topo.Links, &spec.TopologyLink{A: "switch1:Ethernet0", Z: "host1:eth9"})
	err := r.preflightInterfaces()
	if err == nil {
		t.Fatal("host1:eth9 (not in the platform inventory) should fail pre-flight")
	}
	if !strings.Contains(err.Error(), "host1") || !strings.Contains(err.Error(), "eth9") {
		t.Errorf("pre-flight error should name host1/eth9, got: %v", err)
	}
}
