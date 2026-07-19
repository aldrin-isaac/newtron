package newtrun

import (
	"context"
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Intent-snapshot steps give a suite the "am I back where I started?" check.
//
//	- name: capture-baseline
//	  action: snapshot          # GET the device's canonical NEWTRON_INTENT
//	  devices: [switch1]
//	  snapshot: baseline        # store it under this name (run-scoped, cross-scenario)
//
//	- name: assert-clean-return
//	  action: verify-snapshot   # re-read and assert byte-identical to the baseline
//	  devices: [switch1]
//	  snapshot: baseline        # FAILs listing residual/missing/changed records
//
// The store lives on the Runner (cross-scenario within a run — newtrun's
// per-scenario `capture:` map cannot carry a baseline from setup to
// verify-clean). newtron owns the canonical snapshot (drift excludes
// NEWTRON_INTENT, so this is the only read that surfaces a residual/orphaned
// record); newtrun owns naming, storage, and the before/after comparison.

// intentRecords is a device's NEWTRON_INTENT: resource key → fields.
type intentRecords = util.IntentRecords

// storeSnapshot records a named per-device intent snapshot. Safe under the
// parallel per-device goroutines executeForDevices spawns.
func (r *Runner) storeSnapshot(name, device string, snap intentRecords) {
	r.snapshotsMu.Lock()
	defer r.snapshotsMu.Unlock()
	if r.snapshots == nil {
		r.snapshots = map[string]map[string]intentRecords{}
	}
	if r.snapshots[name] == nil {
		r.snapshots[name] = map[string]intentRecords{}
	}
	r.snapshots[name][device] = snap
}

// loadSnapshot returns a previously captured named per-device snapshot.
func (r *Runner) loadSnapshot(name, device string) (intentRecords, bool) {
	r.snapshotsMu.Lock()
	defer r.snapshotsMu.Unlock()
	byDevice, ok := r.snapshots[name]
	if !ok {
		return nil, false
	}
	snap, ok := byDevice[device]
	return snap, ok
}

// snapshotExecutor captures a named per-device intent snapshot.
type snapshotExecutor struct{}

func (e *snapshotExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	name := step.Snapshot
	return r.executeForDevices(step, func(dev string) (string, error) {
		snap, err := r.Client.IntentSnapshot(dev)
		if err != nil {
			return "", err
		}
		r.storeSnapshot(name, dev, snap)
		return fmt.Sprintf("captured %d intent records as %q", len(snap), name), nil
	})
}

// verifySnapshotExecutor re-reads the device intent DB and asserts it is
// byte-identical to a previously captured named snapshot — the residual/
// missing-config check. A non-empty diff FAILs the step, naming the records.
type verifySnapshotExecutor struct{}

func (e *verifySnapshotExecutor) Execute(ctx context.Context, r *Runner, step *Step) *StepOutput {
	name := step.Snapshot
	return r.checkForDevices(step, func(dev string) (StepStatus, string) {
		baseline, ok := r.loadSnapshot(name, dev)
		if !ok {
			return StepStatusError, fmt.Sprintf("no snapshot named %q for %s — capture it first with a snapshot step", name, dev)
		}
		current, err := r.Client.IntentSnapshot(dev)
		if err != nil {
			return StepStatusError, err.Error()
		}
		diff := util.DiffIntentRecords(baseline, current)
		if diff.Empty() {
			return StepStatusPassed, fmt.Sprintf("intent DB matches snapshot %q (%d records)", name, len(current))
		}
		return StepStatusFailed, diff.Summary(fmt.Sprintf("snapshot %q", name))
	})
}
