//go:build integration || e2e

package testutil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/spec"
)

// TestProfile returns a ResolvedProfile pointing at the test Redis container.
func TestProfile() *spec.ResolvedProfile {
	ip := RedisIP()
	return &spec.ResolvedProfile{
		DeviceName:     "test-leaf1",
		MgmtIP:         ip,
		LoopbackIP:     "10.0.0.10",
		Region:         "test-region",
		Site:           "test-site",
		Platform:       "test-platform",
		ASNumber:       13908,
		Affinity:       "east",
		IsRouter:       true,
		IsBridge:       true,
		RouterID:       "10.0.0.10",
		VTEPSourceIP:   "10.0.0.10",
		VTEPSourceIntf: "Loopback0",
		BGPNeighbors:   []string{"10.0.0.1"},
		GenericAlias:   map[string]string{"ce-asnum": "65100"},
		PrefixLists: map[string][]string{
			"rfc1918":      {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
			"mgmt-servers": {"10.100.0.0/24"},
			"bogons":       {"0.0.0.0/8", "127.0.0.0/8", "224.0.0.0/4"},
		},
	}
}

// ConnectedDevice returns a device.Device connected to the test Redis,
// with both DBs seeded with test data. Registers cleanup.
func ConnectedDevice(t *testing.T) *device.Device {
	t.Helper()
	SkipIfNoRedis(t)
	SetupBothDBs(t)

	profile := TestProfile()
	d := device.NewDevice("test-leaf1", profile)

	ctx := Context(t)
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("connecting device: %v", err)
	}

	t.Cleanup(func() {
		d.Disconnect()
	})

	return d
}

// LockedDevice returns a connected and locked device.Device.
func LockedDevice(t *testing.T) *device.Device {
	t.Helper()

	d := ConnectedDevice(t)
	ctx := Context(t)
	if err := d.Lock(ctx); err != nil {
		t.Fatalf("locking device: %v", err)
	}

	t.Cleanup(func() {
		d.Unlock()
	})

	return d
}

// TestNetwork returns a network.Network loaded from testlab specs,
// with the test-leaf1 profile patched to point at the Redis container.
func TestNetwork(t *testing.T) *network.Network {
	t.Helper()
	SkipIfNoRedis(t)

	specsDir := SpecsPath()

	// Patch the test-leaf1 profile to use the actual Redis IP
	patchProfile(t, specsDir, RedisIP())

	net, err := network.NewNetwork(specsDir)
	if err != nil {
		t.Fatalf("creating test network: %v", err)
	}

	return net
}

// ConnectedNetworkDevice returns a connected network.Device backed by test Redis.
func ConnectedNetworkDevice(t *testing.T) *network.Device {
	t.Helper()
	SetupBothDBs(t)

	net := TestNetwork(t)

	ctx := Context(t)
	dev, err := net.ConnectDevice(ctx, "test-leaf1")
	if err != nil {
		t.Fatalf("connecting network device: %v", err)
	}

	t.Cleanup(func() {
		dev.Disconnect()
	})

	return dev
}

// LockedNetworkDevice returns a connected and locked network.Device.
func LockedNetworkDevice(t *testing.T) *network.Device {
	t.Helper()

	dev := ConnectedNetworkDevice(t)
	ctx := Context(t)
	if err := dev.Lock(ctx); err != nil {
		t.Fatalf("locking network device: %v", err)
	}

	t.Cleanup(func() {
		dev.Unlock()
	})

	return dev
}

// patchProfile rewrites the test-leaf1 profile with the actual Redis IP.
func patchProfile(t *testing.T, specsDir, redisIP string) {
	t.Helper()

	profilePath := filepath.Join(specsDir, "profiles", "test-leaf1.json")
	data, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("reading test profile: %v", err)
	}

	var profile map[string]interface{}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("parsing test profile: %v", err)
	}

	profile["mgmt_ip"] = redisIP

	patched, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		t.Fatalf("marshalling test profile: %v", err)
	}

	if err := os.WriteFile(profilePath, patched, 0644); err != nil {
		t.Fatalf("writing test profile: %v", err)
	}
}

// ReconnectDevice disconnects and reconnects a device, reloading state.
func ReconnectDevice(t *testing.T, d *device.Device) {
	t.Helper()
	d.Disconnect()
	ctx := Context(t)
	if err := d.Connect(ctx); err != nil {
		t.Fatalf("reconnecting device: %v", err)
	}
}

// WithCleanState ensures both DBs are flushed and re-seeded.
// Use in subtests that modify Redis state.
func WithCleanState(t *testing.T) {
	t.Helper()
	SetupBothDBs(t)
}

// AssertNoError fails the test if err is not nil.
func AssertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

// AssertError fails the test if err is nil.
func AssertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error but got nil", msg)
	}
}

// Must is a generic helper that calls t.Fatal if err is not nil and returns the value.
func Must[T any](t *testing.T, val T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return val
}

// ignore is used to suppress the unused variable warning in tests.
var _ = context.Background
