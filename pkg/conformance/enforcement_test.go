package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEnforcementLedger verifies the Enforcement column of the newtron
// summary table — the ledger mandated by DESIGN_PRINCIPLES_NEWTRON §49:
//
//   - every principle row declares a class: construction | machine | prose
//   - a machine row names its checker, and the named checker exists — a
//     Test function token must match a real `func TestX(` under pkg/, a
//     .go file token must be a real file under pkg/
//
// A row claiming machine enforcement whose checker does not exist is the
// ledger lying — the exact failure mode the principle exists to prevent.
func TestEnforcementLedger(t *testing.T) {
	root := repoRoot(t)
	newtPath := filepath.Join(root, "docs", "DESIGN_PRINCIPLES_NEWTRON.md")
	data, err := os.ReadFile(newtPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Collect the searchable corpus once: test-func names and file basenames.
	testFuncs := map[string]bool{}
	goFiles := map[string]bool{}
	funcRe := regexp.MustCompile(`func (Test[A-Za-z0-9_]+)\(`)
	err = filepath.WalkDir(filepath.Join(root, "pkg"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		goFiles[filepath.Base(path)] = true
		if strings.HasSuffix(path, "_test.go") {
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range funcRe.FindAllStringSubmatch(string(src), -1) {
				testFuncs[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk pkg/: %v", err)
	}

	testTokRe := regexp.MustCompile(`Test[A-Za-z0-9_]+`)
	fileTokRe := regexp.MustCompile(`[a-z0-9_]+\.go`)

	inSummary := false
	rows := 0
	for line := range strings.SplitSeq(string(data), "\n") {
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
		if len(cells) < 6 || !regexp.MustCompile(`^\d+$`).MatchString(cells[0]) {
			continue // header / separator
		}
		rows++
		num, enforcement := cells[0], cells[len(cells)-2]
		switch {
		case enforcement == "construction" || enforcement == "prose":
			// valid, nothing to verify
		case strings.HasPrefix(enforcement, "machine:"):
			claim := strings.TrimPrefix(enforcement, "machine:")
			verified := false
			for _, tok := range testTokRe.FindAllString(claim, -1) {
				if !testFuncs[tok] {
					t.Errorf("§%s: machine claim names %q — no such test function under pkg/", num, tok)
				} else {
					verified = true
				}
			}
			for _, tok := range fileTokRe.FindAllString(claim, -1) {
				if !goFiles[tok] {
					t.Errorf("§%s: machine claim names %q — no such file under pkg/", num, tok)
				} else {
					verified = true
				}
			}
			if !verified {
				t.Errorf("§%s: machine claim %q names no verifiable checker (Test function or .go file)", num, enforcement)
			}
		default:
			t.Errorf("§%s: enforcement %q is not construction, machine: <checker>, or prose", num, enforcement)
		}
	}
	if rows == 0 {
		t.Fatal("no summary rows parsed — Enforcement column missing or table format changed")
	}
}
