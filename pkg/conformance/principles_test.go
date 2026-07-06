package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// repoRoot locates the repository root relative to this source file, so the
// test finds the docs regardless of the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// pkg/conformance/ → pkg/ → repo root (2 levels up).
	return filepath.Join(filepath.Dir(file), "..", "..")
}

var headingRe = regexp.MustCompile(`^## (\d+)\. (.+)$`)

// parsePrincipleHeadings returns §number → title for every `## N. Title`
// heading in a principles document.
func parsePrincipleHeadings(t *testing.T, path string) map[int]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := map[int]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			if _, dup := out[n]; dup {
				t.Errorf("%s: duplicate principle number §%d", filepath.Base(path), n)
			}
			out[n] = strings.TrimSpace(m[2])
		}
	}
	return out
}

// summaryRow is one data row of a principles summary table.
type summaryRow struct {
	num       int
	universal int  // 0 when the crosswalk cell is — (newtron-only)
	hasXwalk  bool // the row carries a Universal § cell at all
}

// parseSummaryRows returns the data rows of the summary table that follows the
// `# Summary` heading. withCrosswalk demands and parses the trailing
// Universal § cell (newtron doc); without it only the row number is read
// (universal doc).
func parseSummaryRows(t *testing.T, path string, withCrosswalk bool) []summaryRow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	inSummary := false
	var rows []summaryRow
	refRe := regexp.MustCompile(`^§(\d+)$`)
	for _, line := range lines {
		if strings.HasPrefix(line, "# Summary") {
			inSummary = true
			continue
		}
		if !inSummary || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		num, err := strconv.Atoi(cells[0])
		if err != nil {
			continue // header or separator row
		}
		row := summaryRow{num: num}
		if withCrosswalk {
			last := cells[len(cells)-1]
			switch {
			case last == "—":
				row.hasXwalk = true
			case refRe.MatchString(last):
				n, _ := strconv.Atoi(refRe.FindStringSubmatch(last)[1])
				row.universal = n
				row.hasXwalk = true
			}
			if !row.hasXwalk {
				t.Errorf("%s: summary row %d has no Universal § cell (want §N or —, got %q)",
					filepath.Base(path), num, last)
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		t.Fatalf("%s: no summary table rows found after # Summary", filepath.Base(path))
	}
	return rows
}

// assertContiguous fails unless the numbers are exactly 1..len(set).
func assertContiguous(t *testing.T, label string, set map[int]bool) {
	t.Helper()
	for i := 1; i <= len(set); i++ {
		if !set[i] {
			t.Errorf("%s: principle numbers are not contiguous — §%d is missing (have %d principles)",
				label, i, len(set))
			return
		}
	}
}

// TestPrinciplesCrosswalk enforces the two-document contract declared in both
// documents' preambles: the universal doc owns concepts, the newtron doc owns
// applications, and the single copy of the mapping is the Universal § column
// of the newtron summary table.
//
//   - Every principle heading in each document has a summary-table row, and
//     vice versa (no heading without a row, no ghost row).
//   - Principle numbers are contiguous in both documents.
//   - Every newtron summary row declares its universal twin (§N) or declares
//     itself newtron-only (—).
//   - Every declared twin exists as a universal principle heading.
//   - Every universal principle is claimed by at least one newtron principle —
//     a concept with no application does not belong in this repository.
func TestPrinciplesCrosswalk(t *testing.T) {
	root := repoRoot(t)
	uniPath := filepath.Join(root, "docs", "DESIGN_PRINCIPLES.md")
	newtPath := filepath.Join(root, "docs", "DESIGN_PRINCIPLES_NEWTRON.md")

	uniHeadings := parsePrincipleHeadings(t, uniPath)
	newtHeadings := parsePrincipleHeadings(t, newtPath)

	uniSet := map[int]bool{}
	for n := range uniHeadings {
		uniSet[n] = true
	}
	newtSet := map[int]bool{}
	for n := range newtHeadings {
		newtSet[n] = true
	}
	assertContiguous(t, "DESIGN_PRINCIPLES.md", uniSet)
	assertContiguous(t, "DESIGN_PRINCIPLES_NEWTRON.md", newtSet)

	// Summary tables cover exactly the headings.
	uniRows := parseSummaryRows(t, uniPath, false)
	newtRows := parseSummaryRows(t, newtPath, true)

	checkCoverage := func(label string, headings map[int]bool, rows []summaryRow) {
		rowSet := map[int]bool{}
		for _, r := range rows {
			if rowSet[r.num] {
				t.Errorf("%s: duplicate summary row for §%d", label, r.num)
			}
			rowSet[r.num] = true
			if !headings[r.num] {
				t.Errorf("%s: summary row §%d has no matching principle heading", label, r.num)
			}
		}
		for n := range headings {
			if !rowSet[n] {
				t.Errorf("%s: principle §%d has no summary-table row", label, n)
			}
		}
	}
	checkCoverage("DESIGN_PRINCIPLES.md", uniSet, uniRows)
	checkCoverage("DESIGN_PRINCIPLES_NEWTRON.md", newtSet, newtRows)

	// Crosswalk: declared twins exist; every universal concept is claimed.
	claimed := map[int]bool{}
	for _, r := range newtRows {
		if r.universal == 0 {
			continue // declared newtron-only
		}
		if !uniSet[r.universal] {
			t.Errorf("newtron §%d claims universal §%d, which does not exist", r.num, r.universal)
		}
		claimed[r.universal] = true
	}
	for n := range uniSet {
		if !claimed[n] {
			t.Errorf("universal §%d (%s) is claimed by no newtron principle — a concept with no application, or a missing crosswalk entry",
				n, uniHeadings[n])
		}
	}
}
