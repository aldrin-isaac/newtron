package newtrun

import (
	"fmt"
	"os"
	"path/filepath"
)

// SweepAbandonedRuns walks the on-disk state directories and rewrites any
// state.json whose status is "running" to "abandoned". Used at
// newtrun-server startup (issue #29 R1): if a prior server died mid-run,
// the state file still claims "running" but no live process owns the
// work — pause/stop calls return 404 from the in-memory registry while
// the file misleads operators into thinking the run is live.
//
// The sweep is unconditional rather than PID-aware: RunState no longer
// stores a PID (it was removed when state was reduced to abstract run
// identity), and the simpler rule — "if newtrun-server is just starting,
// any running record is by definition stale, because the registry is
// fresh" — holds in practice. The only producer of running records is
// the registry-tracked Runner goroutine; the only consumer that fixes
// them up is the same goroutine on completion. A running record without
// a corresponding registry entry can't be saved by anyone.
//
// Returns the number of records the sweep marked, and the first error
// it encountered (subsequent errors are logged through the caller's
// reporter pattern by surfacing the return value). Errors loading or
// saving a single state file do not abort the sweep — a corrupt
// state.json in one suite directory must not block recovery of another.
func SweepAbandonedRuns() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("newtrun: user home dir: %w", err)
	}
	base := filepath.Join(home, ".newtron", "newtrun")
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return 0, nil
	}

	var (
		marked    int
		firstErr  error
		recordErr = func(err error) {
			if firstErr == nil {
				firstErr = err
			}
		}
	)

	// Suite-namespace records: each immediate subdirectory of base
	// (other than _inline) is a suite-named run.
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, fmt.Errorf("newtrun: read state base: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == "_inline" {
			continue // walked separately below
		}
		n, err := sweepOne(filepath.Join(base, e.Name()), e.Name(), false)
		marked += n
		if err != nil {
			recordErr(err)
		}
	}

	// Inline-namespace records: each subdirectory under _inline is one.
	inline := filepath.Join(base, "_inline")
	if _, err := os.Stat(inline); err == nil {
		inlineEntries, readErr := os.ReadDir(inline)
		if readErr != nil {
			recordErr(fmt.Errorf("newtrun: read inline state base: %w", readErr))
		} else {
			for _, e := range inlineEntries {
				if !e.IsDir() {
					continue
				}
				n, err := sweepOne(filepath.Join(inline, e.Name()), e.Name(), true)
				marked += n
				if err != nil {
					recordErr(err)
				}
			}
		}
	}

	return marked, firstErr
}

// sweepOne examines one state directory. Returns (1, nil) when it
// rewrote a running record to abandoned, (0, nil) when there was no
// running record, and (0, err) on load/save failure. The id parameter
// is used to invoke the right Save* helper for the namespace.
func sweepOne(dir, id string, inline bool) (int, error) {
	state, err := loadStateAt(dir)
	if err != nil {
		return 0, fmt.Errorf("newtrun: load state %s: %w", id, err)
	}
	if state == nil {
		return 0, nil
	}
	if state.Status != SuiteStatusRunning && state.Status != SuiteStatusPausing {
		return 0, nil
	}
	state.Status = SuiteStatusAbandoned
	if inline {
		if err := SaveInlineRunState(state); err != nil {
			return 0, fmt.Errorf("newtrun: save inline state %s: %w", id, err)
		}
	} else {
		if err := SaveRunState(state); err != nil {
			return 0, fmt.Errorf("newtrun: save state %s: %w", id, err)
		}
	}
	return 1, nil
}
