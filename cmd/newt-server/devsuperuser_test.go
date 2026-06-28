package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsNewtronRepoRoot — a directory qualifies as a newtron checkout root only
// when it has BOTH a .git entry and a go.mod declaring the newtron module. This
// is the gate that keeps the developer auto-grant from firing for an installed
// binary that happens to sit next to an unrelated go.mod or .git.
func TestIsNewtronRepoRoot(t *testing.T) {
	const newtronGoMod = "module github.com/aldrin-isaac/newtron\n\ngo 1.24\n"
	const otherGoMod = "module github.com/someone/else\n\ngo 1.24\n"

	cases := []struct {
		name    string
		gitKind string // "dir", "file", or "" for absent
		goMod   string // contents, or "" for absent
		want    bool
	}{
		{"newtron checkout (.git dir)", "dir", newtronGoMod, true},
		{"newtron worktree (.git file)", "file", newtronGoMod, true},
		{"go.mod present but not newtron", "dir", otherGoMod, false},
		{"newtron go.mod but no .git", "", newtronGoMod, false},
		{".git but no go.mod", "dir", "", false},
		{"empty dir", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			switch tc.gitKind {
			case "dir":
				if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: ../x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.goMod != "" {
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(tc.goMod), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := isNewtronRepoRoot(dir); got != tc.want {
				t.Errorf("isNewtronRepoRoot = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFindNewtronRepoRoot — detection walks up from a nested dir (e.g. bin/) to
// the checkout root, and returns "" when no ancestor is a newtron checkout.
func TestFindNewtronRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module github.com/aldrin-isaac/newtron\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// From bin/ (where newt-server lives) detection finds the root above it.
	if got := findNewtronRepoRoot(bin); got != root {
		t.Errorf("findNewtronRepoRoot(bin) = %q, want %q", got, root)
	}
	// A standalone dir with no newtron checkout above it → "".
	if got := findNewtronRepoRoot(t.TempDir()); got != "" {
		t.Errorf("findNewtronRepoRoot(unrelated) = %q, want \"\"", got)
	}
}
