package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
)

// TestOpInverseWireRoute is the §15 pair sweep: every replayable operation's
// declared inverse verb must have a wire surface — a route in the HTTP API.
// A declared inverse with no route is a reverse that exists on paper only;
// the missing-reverse bug class (rollout-admin-status, 2026-06) shipped
// precisely because nothing checked this.
func TestOpInverseWireRoute(t *testing.T) {
	root := repoRoot(t)
	handlerSrc, err := os.ReadFile(filepath.Join(root, "pkg", "newtron", "api", "handler.go"))
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	routes := string(handlerSrc)

	for op, opSpec := range node.RegisteredOps() {
		if opSpec.SideEffect {
			continue
		}
		inv := opSpec.Inverse
		if inv == "reconcile" {
			continue // baseline composites — §15 exception; reconcile has its own routes
		}
		// Inverse form is "scope.verb" (e.g. "device.delete-vrf",
		// "interface.unbind-acl"); the wire route carries the bare verb.
		parts := strings.SplitN(inv, ".", 2)
		if len(parts) != 2 {
			t.Errorf("%s: inverse %q is not scope.verb form", op, inv)
			continue
		}
		verb := parts[1]
		if !strings.Contains(routes, "/"+verb+"\"") && !strings.Contains(routes, "/"+verb+"/") {
			t.Errorf("%s: declared inverse %q has no wire route (/%s) in api/handler.go", op, inv, verb)
		}
	}
}

// TestRecordedParamsClaimed is the §24 write/read claiming sweep, narrowed to
// the concretely checkable class: every intent param the registry declares as
// SourceRecorded exists for reverse-op self-sufficiency or health/status
// reads (§20/§24) — so it must have at least one consumer in the node package
// beyond the registry itself. A recorded param nobody reads is either dead
// storage (§11 speculation) or an unclaimed write — the bgp/check failure
// class, where a written field had readers that didn't know about it.
func TestRecordedParamsClaimed(t *testing.T) {
	root := repoRoot(t)
	nodeDir := filepath.Join(root, "pkg", "newtron", "network", "node")

	// Field-constant map: sonic constant name → literal value, so a consumer
	// using sonic.FieldRouteTargets claims "route_targets".
	constSrc, err := os.ReadFile(filepath.Join(root, "pkg", "newtron", "device", "sonic", "configdb.go"))
	if err != nil {
		t.Fatalf("read configdb.go: %v", err)
	}
	constRe := regexp.MustCompile(`(Field[A-Za-z0-9]+)\s*=\s*"([^"]+)"`)
	valueToConst := map[string]string{}
	for _, m := range constRe.FindAllStringSubmatch(string(constSrc), -1) {
		valueToConst[m[2]] = m[1]
	}

	// Consumer corpus: every node-package .go file except the registry (the
	// declaration site) — op bodies, teardown readers, health checks, tests.
	var corpus strings.Builder
	entries, err := os.ReadDir(nodeDir)
	if err != nil {
		t.Fatalf("read node dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || e.Name() == "op_registry.go" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(nodeDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		corpus.Write(src)
	}
	body := corpus.String()

	claimed := func(key string) bool {
		if strings.Contains(body, `"`+key+`"`) {
			return true
		}
		if c, ok := valueToConst[key]; ok && strings.Contains(body, "sonic."+c) {
			return true
		}
		return false
	}

	for op, opSpec := range node.RegisteredOps() {
		for _, ps := range opSpec.Params {
			if ps.Source != node.SourceRecorded {
				continue
			}
			if !claimed(ps.Key) {
				t.Errorf("%s: recorded param %q has no consumer in the node package outside the registry — dead storage or an unclaimed write (§24)", op, ps.Key)
			}
		}
	}
}
