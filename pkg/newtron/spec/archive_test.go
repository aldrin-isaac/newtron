package spec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestArchiveNetwork_MovesDirIntact pins the soft-delete: the whole spec
// directory (including nested contents like secrets.json and audit/) moves to
// <base>/archives/<id>-<timestamp>/, the source is gone, and nothing is erased.
func TestArchiveNetwork_MovesDirIntact(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "mynet")
	if err := os.MkdirAll(filepath.Join(src, "audit"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A representative payload: a secret, an audit log, a spec file.
	writeFile(t, filepath.Join(src, "secrets.json"), `{"ssh_pass":"s3cr3t"}`)
	writeFile(t, filepath.Join(src, "audit", "audit.log"), `{"event":"x"}`)
	writeFile(t, filepath.Join(src, "network.json"), `{"version":"1.0"}`)

	dst, err := ArchiveNetwork(base, "mynet", "20260703T174500Z")
	if err != nil {
		t.Fatalf("ArchiveNetwork: %v", err)
	}
	wantDst := filepath.Join(base, ArchiveDirName, "mynet-20260703T174500Z")
	if dst != wantDst {
		t.Errorf("archive path = %q, want %q", dst, wantDst)
	}
	// Source gone.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still exists after archive: %v", err)
	}
	// Contents intact at the archive (secret + audit + spec all traveled).
	for _, rel := range []string{"secrets.json", filepath.Join("audit", "audit.log"), "network.json"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("archived %s missing: %v", rel, err)
		}
	}
}

// TestArchiveNetwork_NotFound pins the fail-closed path: archiving a network
// whose spec dir does not exist errors (never a partial/empty move).
func TestArchiveNetwork_NotFound(t *testing.T) {
	base := t.TempDir()
	_, err := ArchiveNetwork(base, "ghost", "20260703T174500Z")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestArchiveNetwork_DestExists pins that a taken destination is refused rather
// than overwriting a prior archive.
func TestArchiveNetwork_DestExists(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "mynet"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create the destination the same timestamp would resolve to.
	if err := os.MkdirAll(filepath.Join(base, ArchiveDirName, "mynet-20260703T174500Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ArchiveNetwork(base, "mynet", "20260703T174500Z")
	if !errors.Is(err, ErrArchiveExists) {
		t.Errorf("err = %v, want ErrArchiveExists", err)
	}
}

// TestIsReservedNetworkName pins that the archive store is the reserved name and
// ordinary ids are not — the single predicate the create path and the discovery
// scan both consult (§13/§15).
func TestIsReservedNetworkName(t *testing.T) {
	if !IsReservedNetworkName(ArchiveDirName) {
		t.Errorf("IsReservedNetworkName(%q) = false, want true", ArchiveDirName)
	}
	for _, ok := range []string{"mynet", "archive", "archives-2", "prod"} {
		if IsReservedNetworkName(ok) {
			t.Errorf("IsReservedNetworkName(%q) = true, want false", ok)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
