package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	newtronapi "github.com/aldrin-isaac/newtron/pkg/newtron/api"
)

// minimal valid spec fixtures the loader accepts (matches the shape
// pkg/newtron/spec/loader.go expects for network.json, topology.json,
// platforms.json). Keep the fixtures small — what we're testing here
// is the discovery + registration path, not spec loading itself.
const (
	fixtureNetworkJSON   = `{"version":"1.0","super_users":[],"user_groups":{},"permissions":{},"zones":{}}`
	fixtureTopologyJSON  = `{"version":"1.0","description":"test","devices":{}}`
	fixturePlatformsJSON = `{"version":"1.0","platforms":{}}`
)

// seedNetwork writes a minimally valid spec tree under
// <base>/<name>/. Returns the dir path.
func seedNetwork(t *testing.T, base, name string, withTopology bool) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Join(dir, "nodes"), 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "network.json"), []byte(fixtureNetworkJSON), 0o644); err != nil {
		t.Fatalf("write network.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "platforms.json"), []byte(fixturePlatformsJSON), 0o644); err != nil {
		t.Fatalf("write platforms.json: %v", err)
	}
	if withTopology {
		if err := os.WriteFile(filepath.Join(dir, "topology.json"), []byte(fixtureTopologyJSON), 0o644); err != nil {
			t.Fatalf("write topology.json: %v", err)
		}
	}
	return dir
}

// TestDiscoverAndRegisterNetworks_RegistersEveryTopology covers the
// happy path: every <base>/<name>/topology.json triggers a
// registration with id=<name>. The discovery output is asserted both
// in the server's registered-network list and in the boot log lines so
// operators see auditable evidence at startup.
func TestDiscoverAndRegisterNetworks_RegistersEveryTopology(t *testing.T) {
	base := t.TempDir()
	seedNetwork(t, base, "net-a", true)
	seedNetwork(t, base, "net-b", true)

	srv := newtronapi.NewServer(newtronapi.Config{Logger: log.New(os.Stderr, "", 0)})
	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	discoverAndRegisterNetworks(srv, base, logger)

	infos := srv.ListNetworks()
	if len(infos) != 2 {
		t.Errorf("registered networks: got %d, want 2; got %+v", len(infos), infos)
	}
	got := map[string]bool{}
	for _, info := range infos {
		got[info.ID] = true
	}
	for _, want := range []string{"net-a", "net-b"} {
		if !got[want] {
			t.Errorf("missing registration for %q", want)
		}
	}
	if !strings.Contains(logBuf.String(), `registered network "net-a"`) {
		t.Errorf("log did not announce net-a registration; got:\n%s", logBuf.String())
	}
}

// TestDiscoverAndRegisterNetworks_SkipsDirsWithoutTopology covers the
// scaffold-in-progress path: a directory without topology.json (e.g.
// `newtrun network create` just landed a name but no substrate yet)
// is skipped, NOT failed. The other networks in the tree register
// normally. Operators can still POST /networks for the skipped one.
func TestDiscoverAndRegisterNetworks_SkipsDirsWithoutTopology(t *testing.T) {
	base := t.TempDir()
	seedNetwork(t, base, "ready", true)
	seedNetwork(t, base, "scaffolding", false) // network.json + platforms.json but no topology.json

	srv := newtronapi.NewServer(newtronapi.Config{Logger: log.New(os.Stderr, "", 0)})
	logger := log.New(os.Stderr, "", 0)
	discoverAndRegisterNetworks(srv, base, logger)

	infos := srv.ListNetworks()
	if len(infos) != 1 || infos[0].ID != "ready" {
		t.Errorf("expected single registration of 'ready'; got %+v", infos)
	}
}

// TestDiscoverAndRegisterNetworks_MissingBaseDirIsNoop covers the
// fresh-install path: --networks-base points at a directory that
// doesn't exist yet. Should register zero networks without an error,
// matching the contract every newt-server flag exposes (every flag
// has a no-config-yet default that boots cleanly).
func TestDiscoverAndRegisterNetworks_MissingBaseDirIsNoop(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	srv := newtronapi.NewServer(newtronapi.Config{Logger: log.New(os.Stderr, "", 0)})
	logger := log.New(os.Stderr, "", 0)

	discoverAndRegisterNetworks(srv, missing, logger)

	if got := srv.ListNetworks(); len(got) != 0 {
		t.Errorf("missing base: expected 0 networks, got %+v", got)
	}
}

// TestDiscoverAndRegisterNetworks_EmptyBaseFlagIsNoop covers the
// explicit-disable path: an operator who sets --networks-base="" wants
// auto-discovery off entirely (any network they want registered, they
// POST). Matches the precedent set by --spec-dir's empty-disables
// behavior before this PR.
func TestDiscoverAndRegisterNetworks_EmptyBaseFlagIsNoop(t *testing.T) {
	srv := newtronapi.NewServer(newtronapi.Config{Logger: log.New(os.Stderr, "", 0)})
	logger := log.New(os.Stderr, "", 0)

	discoverAndRegisterNetworks(srv, "", logger)

	if got := srv.ListNetworks(); len(got) != 0 {
		t.Errorf("empty base: expected 0 networks, got %+v", got)
	}
}

// TestDiscoverAndRegisterNetworks_OneBadEntryDoesNotPoisonRest covers
// the resilience contract: a directory with invalid specs (e.g.
// malformed network.json) is logged as a failure but does not abort
// the boot or block other networks from registering.
func TestDiscoverAndRegisterNetworks_OneBadEntryDoesNotPoisonRest(t *testing.T) {
	base := t.TempDir()
	seedNetwork(t, base, "good", true)

	// Bad: write invalid JSON into network.json
	badDir := filepath.Join(base, "broken")
	if err := os.MkdirAll(filepath.Join(badDir, "nodes"), 0o755); err != nil {
		t.Fatalf("mkdir broken: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "network.json"), []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("write bad network.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "topology.json"), []byte(fixtureTopologyJSON), 0o644); err != nil {
		t.Fatalf("write topology.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "platforms.json"), []byte(fixturePlatformsJSON), 0o644); err != nil {
		t.Fatalf("write platforms.json: %v", err)
	}

	srv := newtronapi.NewServer(newtronapi.Config{Logger: log.New(os.Stderr, "", 0)})
	var logBuf bytes.Buffer
	logger := log.New(&logBuf, "", 0)

	discoverAndRegisterNetworks(srv, base, logger)

	infos := srv.ListNetworks()
	if len(infos) != 1 || infos[0].ID != "good" {
		t.Errorf("expected single registration of 'good' despite bad sibling; got %+v", infos)
	}
	if !strings.Contains(logBuf.String(), "failed to register \"broken\"") {
		t.Errorf("log did not surface broken-network failure; got:\n%s", logBuf.String())
	}
}
