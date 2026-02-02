//go:build e2e

package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type testStatus string

const (
	statusPass    testStatus = "PASS"
	statusFail    testStatus = "FAIL"
	statusSkip    testStatus = "SKIP"
	statusPartial testStatus = "PARTIAL"
)

type testEntry struct {
	name     string
	category string
	node     string
	comment  string
	status   testStatus
	duration time.Duration
	start    time.Time
	order    int
}

type report struct {
	mu      sync.Mutex
	entries map[string]*testEntry
	seq     int
	start   time.Time
}

var globalReport *report

// InitReport creates the global report instance. Call from TestMain before m.Run().
func InitReport() {
	globalReport = &report{
		entries: make(map[string]*testEntry),
		start:   time.Now(),
	}
}

// Track registers a test in the report and sets up a cleanup hook to capture
// the test outcome. Call after SkipIfNoLab(t).
func Track(t *testing.T, category, node string) {
	if globalReport == nil {
		return
	}
	t.Helper()

	globalReport.mu.Lock()
	defer globalReport.mu.Unlock()

	name := t.Name()
	if _, exists := globalReport.entries[name]; exists {
		return
	}

	globalReport.seq++
	entry := &testEntry{
		name:     name,
		category: category,
		node:     node,
		start:    time.Now(),
		order:    globalReport.seq,
	}
	globalReport.entries[name] = entry

	t.Cleanup(func() {
		globalReport.mu.Lock()
		defer globalReport.mu.Unlock()

		entry.duration = time.Since(entry.start)
		switch {
		case t.Failed():
			entry.status = statusFail
		case t.Skipped():
			entry.status = statusSkip
		default:
			entry.status = statusPass
		}
	})
}

// TrackComment attaches a comment to the current test's report entry.
// Call before t.Skip or t.Fatal to capture the reason in the report.
func TrackComment(t *testing.T, msg string) {
	if globalReport == nil {
		return
	}
	t.Helper()

	globalReport.mu.Lock()
	defer globalReport.mu.Unlock()

	if entry, ok := globalReport.entries[t.Name()]; ok {
		if entry.comment != "" {
			entry.comment += "; "
		}
		entry.comment += msg
	}
}

// SetNode updates the node field for a tracked test. Useful when the node
// is not known at Track() time.
func SetNode(t *testing.T, node string) {
	if globalReport == nil {
		return
	}
	t.Helper()

	globalReport.mu.Lock()
	defer globalReport.mu.Unlock()

	if entry, ok := globalReport.entries[t.Name()]; ok {
		entry.node = node
	}
}

// WriteReport computes PARTIAL statuses and writes the markdown report to path.
func WriteReport(path string) error {
	if globalReport == nil {
		return nil
	}

	globalReport.mu.Lock()
	defer globalReport.mu.Unlock()

	computePartialStatuses(globalReport.entries)

	var rows []*testEntry
	for _, e := range globalReport.entries {
		if topLevelName(e.name) == e.name {
			rows = append(rows, e)
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].order < rows[j].order
	})

	var passed, failed, skipped, partial int
	for _, e := range rows {
		switch e.status {
		case statusPass:
			passed++
		case statusFail:
			failed++
		case statusSkip:
			skipped++
		case statusPartial:
			partial++
		}
	}

	totalDuration := time.Since(globalReport.start)

	var sb strings.Builder
	sb.WriteString("# Newtron E2E Test Report\n\n")
	sb.WriteString("| | |\n")
	sb.WriteString("|---|---|\n")
	sb.WriteString(fmt.Sprintf("| **Topology** | %s |\n", LabTopologyName()))
	sb.WriteString(fmt.Sprintf("| **Date** | %s |\n", globalReport.start.UTC().Format("2006-01-02 15:04:05 UTC")))
	sb.WriteString(fmt.Sprintf("| **Total Duration** | %s |\n", formatDuration(totalDuration)))
	sb.WriteString(fmt.Sprintf("| **Passed** | %d |\n", passed))
	sb.WriteString(fmt.Sprintf("| **Failed** | %d |\n", failed))
	sb.WriteString(fmt.Sprintf("| **Skipped** | %d |\n", skipped))
	sb.WriteString(fmt.Sprintf("| **Partial** | %d |\n", partial))
	sb.WriteString("\n## Results\n\n")
	sb.WriteString("| # | Test | Status | Duration | Node | Category | Comments |\n")
	sb.WriteString("|---|------|--------|----------|------|----------|----------|\n")

	for i, e := range rows {
		sb.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %s | %s |\n",
			i+1,
			escapeMarkdownPipe(stripPrefix(e.name)),
			e.status,
			formatDuration(e.duration),
			escapeMarkdownPipe(e.node),
			escapeMarkdownPipe(e.category),
			escapeMarkdownPipe(e.comment),
		))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating report directory: %w", err)
	}

	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// computePartialStatuses walks the entries bottom-up to detect PARTIAL status
// for parent tests with mixed subtest results.
func computePartialStatuses(entries map[string]*testEntry) {
	children := make(map[string][]string)
	for name := range entries {
		parent := directParent(name)
		if parent != "" {
			if _, ok := entries[parent]; ok {
				children[parent] = append(children[parent], name)
			}
		}
	}

	// Sort parents by name length descending (deepest first) for bottom-up processing
	var parents []string
	for name := range children {
		parents = append(parents, name)
	}
	sort.Slice(parents, func(i, j int) bool {
		return len(parents[i]) > len(parents[j])
	})

	for _, parentName := range parents {
		childNames := children[parentName]
		parentEntry := entries[parentName]

		var passes, fails, skips, partials int
		var nonPassing []string

		for _, cn := range childNames {
			child := entries[cn]
			switch child.status {
			case statusPass:
				passes++
			case statusFail:
				fails++
				nonPassing = append(nonPassing, subtestShortName(cn, parentName)+": FAIL")
			case statusSkip:
				skips++
				nonPassing = append(nonPassing, subtestShortName(cn, parentName)+": SKIP")
			case statusPartial:
				partials++
				nonPassing = append(nonPassing, subtestShortName(cn, parentName)+": PARTIAL")
			}
		}

		switch {
		case partials > 0:
			parentEntry.status = statusPartial
		case fails == 0 && skips == 0 && passes > 0:
			parentEntry.status = statusPass
		case passes == 0 && skips == 0 && fails > 0:
			parentEntry.status = statusFail
		case passes == 0 && fails == 0 && skips > 0:
			parentEntry.status = statusSkip
		default:
			parentEntry.status = statusPartial
		}

		if len(nonPassing) > 0 {
			sort.Strings(nonPassing)
			comment := strings.Join(nonPassing, "; ")
			if parentEntry.comment != "" {
				parentEntry.comment += "; " + comment
			} else {
				parentEntry.comment = comment
			}
		}
	}
}

func topLevelName(name string) string {
	if idx := strings.Index(name, "/"); idx >= 0 {
		return name[:idx]
	}
	return name
}

func directParent(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[:idx]
	}
	return ""
}

func stripPrefix(name string) string {
	return strings.TrimPrefix(name, "TestE2E_")
}

func subtestShortName(full, parent string) string {
	if strings.HasPrefix(full, parent+"/") {
		return full[len(parent)+1:]
	}
	return full
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	return fmt.Sprintf("%dm %ds", m, s)
}

func escapeMarkdownPipe(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
