package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// helper — decodes the {"data":...} envelope into the typed result.
func decodeEnvelope(t *testing.T, body []byte, into any) {
	t.Helper()
	var env httputil.APIResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	if env.Error != "" {
		t.Fatalf("envelope error: %s", env.Error)
	}
	if into == nil {
		return
	}
	data, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatalf("re-marshal data: %v", err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("decode data into %T: %v", into, err)
	}
}

func TestBridgeStatsStoreSetAndGet(t *testing.T) {
	store := NewBridgeStatsStore()
	store.Set("lab-a", "", newtlab.BridgeStats{
		Links: []newtlab.LinkStats{{A: "spine1:Ethernet0", Z: "leaf1:Ethernet0", Connected: true}},
	})
	store.Set("lab-a", "host-b", newtlab.BridgeStats{
		Links: []newtlab.LinkStats{{A: "spine1:Ethernet4", Z: "leaf2:Ethernet0", Connected: true}},
	})

	snaps := store.Get("lab-a")
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(snaps))
	}
	// Stable order by host string — "" sorts before "host-b".
	if snaps[0].Host != "" || snaps[1].Host != "host-b" {
		t.Errorf("host order = %q,%q want \"\",\"host-b\"", snaps[0].Host, snaps[1].Host)
	}
	if snaps[0].AgeSeconds < 0 || snaps[1].AgeSeconds < 0 {
		t.Errorf("AgeSeconds must be non-negative; got %v / %v", snaps[0].AgeSeconds, snaps[1].AgeSeconds)
	}
	if !snaps[0].Stats.Links[0].Connected {
		t.Errorf("local snapshot lost Connected=true")
	}
}

func TestBridgeStatsStoreGetEmpty(t *testing.T) {
	store := NewBridgeStatsStore()
	snaps := store.Get("unknown-lab")
	if snaps == nil {
		t.Fatal("Get must return empty slice, not nil")
	}
	if len(snaps) != 0 {
		t.Errorf("want empty, got %d entries", len(snaps))
	}
}

func TestBridgeStatsStoreEvictLab(t *testing.T) {
	store := NewBridgeStatsStore()
	store.Set("lab-a", "", newtlab.BridgeStats{})
	store.Set("lab-b", "", newtlab.BridgeStats{})
	store.EvictLab("lab-a")

	if got := len(store.Get("lab-a")); got != 0 {
		t.Errorf("after evict, lab-a has %d snapshots, want 0", got)
	}
	if got := len(store.Get("lab-b")); got != 1 {
		t.Errorf("evict bled into lab-b: got %d snapshots", got)
	}
}

func TestBridgeStatsStoreOverwriteSameHost(t *testing.T) {
	store := NewBridgeStatsStore()
	store.Set("lab-a", "", newtlab.BridgeStats{Links: []newtlab.LinkStats{{A: "v1"}}})
	first := store.Get("lab-a")[0].UpdatedAt
	// Tiny sleep so the timestamps differ — without it, the test
	// would only verify the body changed, not that UpdatedAt advanced.
	time.Sleep(2 * time.Millisecond)
	store.Set("lab-a", "", newtlab.BridgeStats{Links: []newtlab.LinkStats{{A: "v2"}}})
	snaps := store.Get("lab-a")

	if len(snaps) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snaps))
	}
	if snaps[0].Stats.Links[0].A != "v2" {
		t.Errorf("second Set didn't overwrite; got A = %q", snaps[0].Stats.Links[0].A)
	}
	if snaps[0].UpdatedAt == first {
		t.Errorf("UpdatedAt did not advance on overwrite")
	}
}

// TestPushBridgeStatsRoundTrip verifies the POST endpoint stores what
// newtlink would send, and the GET endpoint returns it in the wire shape
// the CLI consumes.
func TestPushBridgeStatsRoundTrip(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	payload := newtlab.BridgeStats{
		Links: []newtlab.LinkStats{
			{A: "spine1:Ethernet0", Z: "leaf1:Ethernet0", AToZBytes: 1234, ZToABytes: 5678, Sessions: 1, Connected: true},
		},
	}
	body, _ := json.Marshal(payload)

	pushURL := ts.URL + "/newtlab/v1/labs/lab-a/bridges/local/stats"
	resp, err := ts.Client().Post(pushURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("POST status = %d, want 204", resp.StatusCode)
	}

	getResp, err := ts.Client().Get(ts.URL + "/newtlab/v1/labs/lab-a/bridges/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != 200 {
		t.Fatalf("GET status = %d, want 200", getResp.StatusCode)
	}

	var snaps []BridgeStatsSnapshot
	rawBody := readAll(t, getResp)
	decodeEnvelope(t, rawBody, &snaps)
	if len(snaps) != 1 {
		t.Fatalf("got %d snapshots, want 1; body: %s", len(snaps), rawBody)
	}
	// "local" must map back to "" in storage so the wire shape
	// matches BridgeState.Bridges[""] semantics elsewhere in newtlab.
	if snaps[0].Host != "" {
		t.Errorf("Host = %q, want empty for the local worker", snaps[0].Host)
	}
	if snaps[0].Stats.Links[0].AToZBytes != 1234 {
		t.Errorf("AToZBytes = %d, want 1234", snaps[0].Stats.Links[0].AToZBytes)
	}
	if snaps[0].UpdatedAt == "" {
		t.Error("UpdatedAt was not set")
	}
}

func TestPushBridgeStatsRejectsBadJSON(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/newtlab/v1/labs/lab-a/bridges/local/stats",
		"application/json", bytes.NewReader([]byte("{not valid json")))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetBridgeStatsEmptyForUnknownLab(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/newtlab/v1/labs/nope/bridges/stats")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 with empty list (404 reserved for missing labs)", resp.StatusCode)
	}
	var snaps []BridgeStatsSnapshot
	decodeEnvelope(t, readAll(t, resp), &snaps)
	if len(snaps) != 0 {
		t.Errorf("got %d snapshots, want 0", len(snaps))
	}
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.Bytes()
}
