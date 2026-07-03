package main

import (
	"log"
	"os"
	"path/filepath"
	"sort"

	newtronapi "github.com/aldrin-isaac/newtron/pkg/newtron/api"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// discoverAndRegisterNetworks scans <networksBase>/<name>/topology.json
// at boot and registers each as a network with id=<name> on the newtron
// engine. Mirrors the layout newtlab has always read from
// (cmd/newtlab/main.go:282-308) so the two engines share one
// discovery convention — §27 (Single Owner) and §13 (Same Concept =
// Same Name).
//
// Missing networksBase is non-fatal: a fresh install with no networks
// tree just registers zero networks. Failed registrations on
// individual subdirs are logged but do not abort startup — one
// malformed network shouldn't keep the rest of the tree unreachable.
//
// Registration is idempotent on matching dir, so later
// `bin/newtlab deploy <name>` calls (which also call
// client.RegisterNetwork) against an already-registered network are
// no-ops rather than conflicts.
func discoverAndRegisterNetworks(srv *newtronapi.Server, networksBase string, logger *log.Logger) {
	if networksBase == "" {
		return
	}
	entries, err := os.ReadDir(networksBase)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		logger.Printf("auto-discovery: reading %s: %v (continuing without auto-discovered networks)", networksBase, err)
		return
	}

	// Sort so the registration log lines come out in a stable order —
	// makes the startup banner readable and the boot log easier to diff
	// across runs.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		// The archive store is a reserved subdirectory, not a network — never
		// register it or anything under it (§15: discovery skips what the create
		// path rejects). It has no topology.json at its top anyway, but skip it
		// explicitly so a future recursive scan can't resurrect archived networks.
		if spec.IsReservedNetworkName(name) {
			continue
		}
		dir := filepath.Join(networksBase, name)
		// The marker file is topology.json — same shape newtlab uses
		// to decide a directory is a deployable network. Networks
		// without topology.json are not auto-registered (could be a
		// scaffold in progress, or a network that was scaffolded by
		// `newtrun network create` but doesn't yet have a substrate).
		// Operators can still POST /networks for those if they want
		// the slot live before topology.json lands.
		if _, err := os.Stat(filepath.Join(dir, "topology.json")); err != nil {
			continue
		}
		if err := srv.RegisterNetwork(name, dir); err != nil {
			logger.Printf("auto-discovery: failed to register %q from %s: %v", name, dir, err)
			continue
		}
		logger.Printf("auto-discovery: registered network %q from %s", name, dir)
	}
}
