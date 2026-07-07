package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
)

// Wire-body tests for the update verbs whose client-side send path
// previously hand-rolled partial bodies (RCA-049 third instance: the Go
// client's update-bgp-evpn-peer body dropped the evpn flag even after the
// handler was fixed). The client now sends the canonical config verbatim;
// these pin the fields that died on the old path.

// captureServer records the body of the first non-GET request.
func captureServer(t *testing.T) (*httptest.Server, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			raw, _ := io.ReadAll(r.Body)
			mu.Lock()
			if body == nil {
				body = map[string]any{}
				_ = json.Unmarshal(raw, &body)
			}
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"data":{"applied":true}}`))
	}))
	return ts, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return body
	}
}

func TestUpdateBGPEVPNPeer_EVPNFlagOnTheWire(t *testing.T) {
	ts, body := captureServer(t)
	defer ts.Close()

	c := New(ts.URL, "net")
	_, err := c.UpdateBGPEVPNPeer("leaf1", "10.0.0.2", newtron.BGPNeighborConfig{
		RemoteAS:    65002,
		Description: "d",
		EVPN:        true,
	}, newtron.ExecOpts{Execute: true})
	if err != nil {
		t.Fatalf("UpdateBGPEVPNPeer: %v", err)
	}

	b := body()
	if b["evpn"] != true {
		t.Errorf("evpn flag missing from wire body: %v", b)
	}
	if b["neighbor_ip"] != "10.0.0.2" {
		t.Errorf("neighbor_ip = %v, want the method's ip argument", b["neighbor_ip"])
	}
}

func TestInterfaceUpdateBGPPeer_FullConfigOnTheWire(t *testing.T) {
	ts, body := captureServer(t)
	defer ts.Close()

	c := New(ts.URL, "net")
	_, err := c.InterfaceUpdateBGPPeer("leaf1", "Ethernet0", newtron.BGPNeighborConfig{
		NeighborIP:  "10.1.0.1",
		RemoteAS:    65002,
		Description: "d",
		Multihop:    2,
	}, newtron.ExecOpts{Execute: true})
	if err != nil {
		t.Fatalf("InterfaceUpdateBGPPeer: %v", err)
	}

	b := body()
	for _, key := range []string{"neighbor_ip", "remote_as", "description", "multihop"} {
		if _, ok := b[key]; !ok {
			t.Errorf("field %q missing from wire body: %v", key, b)
		}
	}
}

func TestUpdateACLRule_FullBodyOnTheWire(t *testing.T) {
	ts, body := captureServer(t)
	defer ts.Close()

	c := New(ts.URL, "net")
	_, err := c.UpdateACLRule("leaf1", "EDGE", newtron.ACLRuleUpdateRequest{
		RuleName: "RULE_10",
		Priority: 100,
		Action:   "FORWARD",
		SrcIP:    "10.0.0.0/8",
		Protocol: "tcp",
		DstPort:  "443",
	}, newtron.ExecOpts{Execute: true})
	if err != nil {
		t.Fatalf("UpdateACLRule: %v", err)
	}

	b := body()
	if b["acl"] != "EDGE" {
		t.Errorf("acl = %v, want the method's acl argument to win", b["acl"])
	}
	for _, key := range []string{"rule_name", "priority", "action", "src_ip", "protocol", "dst_port"} {
		if _, ok := b[key]; !ok {
			t.Errorf("field %q missing from wire body: %v", key, b)
		}
	}
	if strings.Contains(strings.Join(mapKeys(b), ","), "ACLName") {
		t.Errorf("Go field names leaked into the wire (missing json tags): %v", b)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
