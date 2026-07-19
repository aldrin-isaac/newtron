package util

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// IntentRecords is a device's NEWTRON_INTENT table in canonical form:
// resource key → fields (DAG links normalized). It is the substrate for
// "is the device back where it started?" before/after comparisons.
type IntentRecords = map[string]map[string]string

// IntentDiff is the divergence between two canonical intent snapshots.
//   - Added: present now, absent from the baseline — residual left on the device
//     (e.g. a forward op whose reverse never ran, or a reverse that under-reaped).
//   - Removed: present in the baseline, gone now — config the device lost.
//   - Changed: same key, different fields.
type IntentDiff struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

// Empty reports whether the two snapshots are identical.
func (d IntentDiff) Empty() bool {
	return len(d.Added)+len(d.Removed)+len(d.Changed) == 0
}

// Summary renders a readable one-line divergence report. label names the
// baseline being compared against.
func (d IntentDiff) Summary(label string) string {
	if d.Empty() {
		return fmt.Sprintf("intent DB matches %s", label)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "device diverged from %s —", label)
	if len(d.Added) > 0 {
		fmt.Fprintf(&b, " residual (+%d): %v;", len(d.Added), d.Added)
	}
	if len(d.Removed) > 0 {
		fmt.Fprintf(&b, " missing (-%d): %v;", len(d.Removed), d.Removed)
	}
	if len(d.Changed) > 0 {
		fmt.Fprintf(&b, " changed (~%d): %v;", len(d.Changed), d.Changed)
	}
	return b.String()
}

// DiffIntentRecords compares two canonical intent snapshots. Both sides must
// already be normalized (see node.NormalizeIntentFields / the intent-snapshot
// endpoint) so DAG-link ordering does not produce false differences.
func DiffIntentRecords(baseline, current IntentRecords) IntentDiff {
	var d IntentDiff
	for k, cur := range current {
		base, ok := baseline[k]
		if !ok {
			d.Added = append(d.Added, k)
		} else if !reflect.DeepEqual(base, cur) {
			d.Changed = append(d.Changed, k)
		}
	}
	for k := range baseline {
		if _, ok := current[k]; !ok {
			d.Removed = append(d.Removed, k)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	return d
}
