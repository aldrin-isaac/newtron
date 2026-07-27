package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// TestTopologyCRUD_AddLink_ValidationErrorsAreTyped pins #401: every
// AddTopologyLink input rejection is a typed *util.ValidationError (→ 400
// through httpStatusFromError), with Field naming the offending request
// field ("a"/"z"). Before, these were plain fmt.Errorf → 500, which consoles
// mapping 5xx to "engine unavailable" showed as an outage.
func TestTopologyCRUD_AddLink_ValidationErrorsAreTyped(t *testing.T) {
	specDir := copyTestSpecDir(t)
	if err := os.WriteFile(
		filepath.Join(specDir, "nodes", "switch2.json"),
		[]byte(`{"mgmt_ip":"127.0.0.1","loopback_ip":"10.0.0.2","zone":"amer","platform":"sonic-vs","ssh_user":"admin","ssh_pass":"x","underlay_asn":65002}`),
		0o644,
	); err != nil {
		t.Fatalf("write nodeSpec: %v", err)
	}
	net, err := newtron.LoadNetwork(specDir, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadNetwork: %v", err)
	}
	dev := &spec.TopologyNode{
		Ports: map[string]*spec.PortConfig{"Ethernet0": {AdminStatus: "up"}},
	}
	if err := net.AddTopologyDevice(context.Background(), "switch2", dev); err != nil {
		t.Fatalf("AddTopologyDevice: %v", err)
	}

	cases := []struct {
		name      string
		link      *spec.TopologyLink
		wantField string
	}{
		{"undeclared interface on z", &spec.TopologyLink{A: "switch2:Ethernet0", Z: "switch2:Ethernet99"}, "z"},
		{"malformed endpoint on a", &spec.TopologyLink{A: "no-colon-here", Z: "switch2:Ethernet0"}, "a"},
		{"unknown device on a", &spec.TopologyLink{A: "ghost:Ethernet0", Z: "switch2:Ethernet0"}, "a"},
		{"empty endpoints", &spec.TopologyLink{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := net.AddTopologyLink(context.Background(), c.link)
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			var ve *util.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v (%T); want *util.ValidationError", err, err)
			}
			if ve.Field != c.wantField {
				t.Errorf("Field = %q, want %q", ve.Field, c.wantField)
			}
			if got := httpStatusFromError(err); got != 400 {
				t.Errorf("httpStatusFromError = %d, want 400", got)
			}
		})
	}
}

// TestWriteErrorValidationEnvelope pins the #464 wire contract: a wrapped
// *util.ValidationError produces 400 with the typed {field, errors} payload
// in data, so a client attributes the rejection to the offending field
// without parsing the message (§46).
func TestWriteErrorValidationEnvelope(t *testing.T) {
	// The exact wrap shape the apply-service path produces:
	// "BGP peering config for Ethernet0: %w" around the typed error.
	inner := &util.ValidationError{Field: "peer_as", Errors: []string{"service requires peer_as parameter"}}
	err := fmt.Errorf("BGP peering config for Ethernet0: %w", inner)

	rec := httptest.NewRecorder()
	writeError(rec, err)

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var env httputil.APIResponse
	if jsonErr := json.Unmarshal(rec.Body.Bytes(), &env); jsonErr != nil {
		t.Fatalf("decode envelope: %v", jsonErr)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %T (%v); want object", env.Data, env.Data)
	}
	if data["field"] != "peer_as" {
		t.Errorf("data.field = %v, want peer_as", data["field"])
	}
	errs, _ := data["errors"].([]any)
	if len(errs) != 1 || errs[0] != "service requires peer_as parameter" {
		t.Errorf("data.errors = %v", data["errors"])
	}
}
