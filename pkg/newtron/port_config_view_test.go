package newtron

import (
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// TestToPortConfigView pins the spec.PortConfig → public PortConfig mirror at
// the API boundary (§33): every field copies faithfully, and a nil entry maps
// to nil so a null port round-trips rather than becoming an empty struct.
func TestToPortConfigView(t *testing.T) {
	in := &spec.PortConfig{AdminStatus: "up", MTU: 9100, Speed: "100G", Description: "uplink"}
	got := toPortConfigView(in)
	if got == nil {
		t.Fatal("toPortConfigView returned nil for a non-nil input")
	}
	if got.AdminStatus != "up" || got.MTU != 9100 || got.Speed != "100G" || got.Description != "uplink" {
		t.Errorf("mirror = %+v, want {up 9100 100G uplink}", *got)
	}
	if toPortConfigView(nil) != nil {
		t.Error("toPortConfigView(nil) should be nil (null port round-trips as nil)")
	}
}
