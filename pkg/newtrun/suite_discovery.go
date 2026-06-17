// Suite-name → on-disk path resolution against the per-topology layout.
//
// Suites live at <topologies-base>/<topology>/suites/<suite-name>/. The
// `topology:` field inside suite.yaml is the authoritative declaration;
// the directory tree mirrors that declaration structurally per §27
// (Single Owner) — a suite tied to topology X belongs in X's tree, not
// in a flat sibling dir that floats above all topologies.
//
// Suite names remain globally unique across the tree so the
// `bin/newtrun start <name>` and `GET /newtrun/v1/runs/<name>` URLs
// stay flat. ResolveSuiteDir enforces the uniqueness — two topologies
// with a same-named suite is an operator misconfiguration the server
// surfaces explicitly rather than silently picking one.
//
// Lives at the pkg/newtrun layer (not pkg/newtrun/api) so the in-process
// run-suite step (steps_run_suite.go) and the HTTP layer share one
// resolver — §27 forbids two implementations of the same concept.
package newtrun

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ResolveSuiteDir returns the on-disk path of the suite named `suite`.
// Globs <topologies-base>/*/suites/<suite>/.
//
//   - Zero matches → os.ErrNotExist (wrapped with the suite name so a
//     404 handler can include it in the response).
//   - One match → that path.
//   - More than one match → an explicit ambiguity error naming the
//     conflicting topologies. Suite names must be unique across the
//     tree so the flat `bin/newtrun start <name>` URL shape stays
//     unambiguous.
func ResolveSuiteDir(topologiesBase, suite string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(topologiesBase, "*", "suites", suite))
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("suite %q: %w", suite, os.ErrNotExist)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("suite %q is defined in %d topologies (%v); suite names must be unique across the topologies tree", suite, len(matches), matches)
	}
}

// ListAllSuites returns every suite name discoverable under
// topologiesBase. Scans <topologies-base>/*/suites/* and returns the
// sorted set of basenames. Missing topologies-base directories return
// an empty slice rather than an error — a fresh checkout with no
// topologies tree is a valid deployment state.
func ListAllSuites(topologiesBase string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(topologiesBase, "*", "suites", "*"))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil || !info.IsDir() {
			continue
		}
		names = append(names, filepath.Base(m))
	}
	sort.Strings(names)
	return names, nil
}
