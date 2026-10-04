package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// TestServiceProjection_SeesSpecChangeSinceLastOperation pins that the service
// projection, which reads every node a NodeActor holds, starts like any other
// operation (DESIGN_PRINCIPLES_NEWTRON §7): it resolves the node's specs as they stand
// now, not as they stood at the node's last operation.
func TestServiceProjection_SeesSpecChangeSinceLastOperation(t *testing.T) {
	s, _ := newPersistTestServer(t)

	// A routed BGP service, applied in topology mode so a NodeActor holds a
	// node for switch1. The neighbor's asn comes from the service spec.
	svc := newtron.CreateServiceRequest{Name: "BSVC", ServiceType: "routed",
		Routing: &newtron.CreateServiceRouting{Protocol: "bgp", PeerAS: "65010"}}
	if w := httpPostJSON(t, s, "/newtron/v1/networks/default/create-service", svc); w.Code != http.StatusCreated {
		t.Fatalf("create-service: %d %s", w.Code, w.Body.String())
	}
	if w := httpPostJSON(t, s, "/newtron/v1/networks/default/nodes/switch1/interfaces/Ethernet0/apply-service?mode=topology",
		ApplyServiceRequest{Service: "BSVC", IPAddress: "10.9.0.0/31"}); w.Code != http.StatusOK {
		t.Fatalf("apply-service: %d %s", w.Code, w.Body.String())
	}

	// The service changes after the node's last operation.
	svc.Routing.PeerAS = "65020"
	if w := httpPostJSON(t, s, "/newtron/v1/networks/default/update-service", svc); w.Code != http.StatusOK {
		t.Fatalf("update-service: %d %s", w.Code, w.Body.String())
	}

	w := httpDo(t, s, http.MethodGet, "/newtron/v1/networks/default/services/BSVC/projection")
	if w.Code != http.StatusOK {
		t.Fatalf("projection: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data newtron.ServiceProjectionResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var asn string
	for _, n := range env.Data.Nodes {
		for _, d := range n.Diff {
			if d.Table == "BGP_NEIGHBOR" && d.Key == "default|10.9.0.1" {
				asn = d.Expected["asn"]
			}
		}
	}
	if asn != "65020" {
		t.Errorf("projection's neighbor asn = %q, want the updated service's 65020", asn)
	}
}
