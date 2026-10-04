package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// propertyTestDevice is testDevice on a platform whose ports default to
// defaultSpeed ("" for a platform that declares none).
func propertyTestDevice(defaultSpeed string) *Node {
	n := testDevice()
	n.resolved.Platform = "test-platform"
	n.SpecProvider.(*testSpecProvider).platforms["test-platform"] = &spec.PlatformSpec{
		Name: "test-platform", DefaultSpeed: defaultSpeed,
	}
	return n
}

// A speed reaches PORT in Mbps — the only form orchagent parses. Writing the
// authored "100G" is the RCA-050 failure: on CiscoVS one unparseable port
// stalls every port after the next config reload (#530).
func TestSetProperty_SpeedWrittenInMbps(t *testing.T) {
	n := propertyTestDevice("40G")
	i, _ := n.GetInterface("Ethernet0")

	cs, err := i.SetProperty(context.Background(), "speed", "100G")
	if err != nil {
		t.Fatalf("SetProperty speed: %v", err)
	}
	c := assertChange(t, cs, "PORT", "Ethernet0", ChangeModify)
	if got := c.Fields["speed"]; got != "100000" {
		t.Errorf("PORT speed = %q, want %q", got, "100000")
	}

	intent := n.GetIntent("interface|Ethernet0|speed")
	if intent == nil {
		t.Fatal("no speed intent recorded")
	}
	if got := intent.Params[sonic.FieldValue]; got != "100G" {
		t.Errorf("recorded value = %q, want the authored %q", got, "100G")
	}
	if got := intent.Params[sonic.FieldDefaultSpeed]; got != "40000" {
		t.Errorf("recorded default_speed = %q, want %q", got, "40000")
	}
}

// Clearing a speed override restores the platform default the port inherits,
// from the intent record alone (§20) — never "", which orchagent refuses.
func TestClearProperty_SpeedRestoresPlatformDefault(t *testing.T) {
	ctx := context.Background()
	n := propertyTestDevice("40G")
	i, _ := n.GetInterface("Ethernet0")
	if _, err := i.SetProperty(ctx, "speed", "100G"); err != nil {
		t.Fatalf("SetProperty speed: %v", err)
	}

	// The reverse must not consult the platform spec: remove it first.
	delete(n.SpecProvider.(*testSpecProvider).platforms, "test-platform")

	cs, err := i.ClearProperty(ctx, "speed")
	if err != nil {
		t.Fatalf("ClearProperty speed: %v", err)
	}
	c := assertChange(t, cs, "PORT", "Ethernet0", ChangeModify)
	if got := c.Fields["speed"]; got != "40000" {
		t.Errorf("cleared PORT speed = %q, want the platform default %q", got, "40000")
	}
	if n.GetIntent("interface|Ethernet0|speed") != nil {
		t.Error("speed intent should be deleted by clear-property")
	}
}

// A speed override is refused where it could not be reverted, before anything
// is written.
func TestSetProperty_SpeedRefusedWithoutPlatformDefault(t *testing.T) {
	n := propertyTestDevice("")
	i, _ := n.GetInterface("Ethernet0")

	_, err := i.SetProperty(context.Background(), "speed", "100G")
	if err == nil || !strings.Contains(err.Error(), "default_speed") {
		t.Fatalf("err = %v, want a refusal naming default_speed", err)
	}
	// A precondition, so the API answers 409 and replay skips the intent
	// instead of aborting the device's whole rebuild.
	if !errors.Is(err, util.ErrPreconditionFailed) {
		t.Errorf("err = %v, want a precondition failure", err)
	}
	if n.GetIntent("interface|Ethernet0|speed") != nil {
		t.Error("a refused set-property must not leave an intent behind")
	}
}

// Values are validated by spec.PortConfig, the owner shared with the topology's
// port config, before any intent is written.
func TestSetProperty_RejectsInvalidValues(t *testing.T) {
	tests := []struct{ property, value string }{
		{"speed", "100000"}, // the wire form is not the authored vocabulary
		{"speed", "7G"},
		{"mtu", "nine"},
		{"mtu", "0"},
		{"mtu", "9217"},
		{"admin_status", "sideways"},
		{"description", ""},
		{"color", "blue"},
	}
	for _, tt := range tests {
		n := propertyTestDevice("40G")
		i, _ := n.GetInterface("Ethernet0")
		_, err := i.SetProperty(context.Background(), tt.property, tt.value)
		var verr *util.ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("SetProperty(%s, %q): err = %v, want a validation error (400)", tt.property, tt.value, err)
		}
		if n.GetIntent("interface|Ethernet0|"+tt.property) != nil {
			t.Errorf("SetProperty(%s, %q): a refused write left an intent behind", tt.property, tt.value)
		}
	}
}

// The other properties render as before.
func TestSetProperty_OtherPropertiesRender(t *testing.T) {
	tests := []struct{ property, value, field, want string }{
		{"mtu", "9000", "mtu", "9000"},
		{"admin_status", "down", "admin_status", "down"},
		{"admin-status", "up", "admin_status", "up"},
		{"description", "uplink", "description", "uplink"},
	}
	for _, tt := range tests {
		n := propertyTestDevice("40G")
		i, _ := n.GetInterface("Ethernet0")
		cs, err := i.SetProperty(context.Background(), tt.property, tt.value)
		if err != nil {
			t.Fatalf("SetProperty(%s, %q): %v", tt.property, tt.value, err)
		}
		c := assertChange(t, cs, "PORT", "Ethernet0", ChangeModify)
		if got := c.Fields[tt.field]; got != tt.want {
			t.Errorf("SetProperty(%s, %q): PORT %s = %q, want %q", tt.property, tt.value, tt.field, got, tt.want)
		}
		if _, ok := n.GetIntent("interface|Ethernet0|" + tt.property).Params[sonic.FieldDefaultSpeed]; ok {
			t.Errorf("SetProperty(%s): default_speed recorded on a non-speed property", tt.property)
		}
	}
}
