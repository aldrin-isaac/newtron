package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/audit"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

const testStamp = "20260703T174500Z"

// scaffoldNetwork lays down a registerable spec dir under base and returns its path.
func scaffoldNetworkDir(t *testing.T, base, id string) string {
	t.Helper()
	dir := filepath.Join(base, id)
	if err := spec.CreateEmpty(dir, ""); err != nil {
		t.Fatalf("CreateEmpty: %v", err)
	}
	return dir
}

// TestDeleteNetwork_ArchivesUnregistered pins the happy path: an unregistered
// on-disk network is archived to <base>/archives/<id>-<ts>, and the source dir
// is gone. The existence layer works with no serving-layer involvement.
func TestDeleteNetwork_ArchivesUnregistered(t *testing.T) {
	base := t.TempDir()
	src := scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{NetworksBase: base})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	dst, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp)
	if err != nil {
		t.Fatalf("DeleteNetwork: %v", err)
	}
	if want := filepath.Join(base, spec.ArchiveDirName, "mynet-"+testStamp); dst != want {
		t.Errorf("archive path = %q, want %q", dst, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source dir still present after archive: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("archived dir missing: %v", err)
	}
}

// TestDeleteNetwork_RefusesRegistered pins the layer separation: a network that
// is still registered (served) cannot be archived — the caller must unregister
// first. 409 ConflictError.
func TestDeleteNetwork_RefusesRegistered(t *testing.T) {
	base := t.TempDir()
	dir := scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{NetworksBase: base})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if err := s.RegisterNetwork("mynet", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}

	_, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp)
	var ce *util.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *util.ConflictError (still registered)", err)
	}
	// And the dir was NOT moved.
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("dir should be untouched while registered: %v", statErr)
	}
}

// TestDeleteNetwork_NotFound pins that archiving a network with no spec dir is a
// clean 404, not a partial move.
func TestDeleteNetwork_NotFound(t *testing.T) {
	s := NewServer(Config{NetworksBase: t.TempDir()})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	_, err := s.DeleteNetwork(context.Background(), "ghost", false, testStamp)
	var nf *newtron.NotFoundError
	if !errors.As(err, &nf) {
		t.Errorf("err = %v, want *newtron.NotFoundError", err)
	}
}

// TestDeleteNetwork_LabGuard pins the cross-engine guard: a deployed lab blocks
// the delete (409) unless force; force archives anyway.
func TestDeleteNetwork_LabGuard(t *testing.T) {
	base := t.TempDir()
	scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{
		NetworksBase: base,
		LabDeployed:  func(context.Context, string) (bool, error) { return true, nil },
	})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	_, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp)
	var ce *util.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *util.ConflictError (lab deployed)", err)
	}
	// force bypasses the lab guard.
	if _, err := s.DeleteNetwork(context.Background(), "mynet", true, testStamp); err != nil {
		t.Errorf("force delete with lab deployed = %v, want nil", err)
	}
}

// TestDeleteNetwork_LabCheckErrorFailsClosed pins that an error probing lab state
// refuses the delete (never archives on uncertainty).
func TestDeleteNetwork_LabCheckErrorFailsClosed(t *testing.T) {
	base := t.TempDir()
	dir := scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{
		NetworksBase: base,
		LabDeployed:  func(context.Context, string) (bool, error) { return false, errors.New("newtlab unreachable") },
	})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if _, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp); err == nil {
		t.Error("lab-check error should refuse the delete; got nil")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("dir should be untouched when the lab check errors: %v", statErr)
	}
}

// TestAuthorizeRegistry pins the registry gate (create + delete): global
// super-users pass, everyone else is denied, and enforcement-off is a no-op.
func TestAuthorizeRegistry(t *testing.T) {
	withCaller := func(name string) context.Context {
		return audit.WithCaller(context.Background(), &audit.Caller{Username: name})
	}

	s := NewServer(Config{EnforceAuthorization: true, GlobalSuperUsers: []string{"ron"}})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if err := s.authorizeRegistry(withCaller("ron"), "network.create"); err != nil {
		t.Errorf("global super-user ron denied: %v", err)
	}
	if err := s.authorizeRegistry(withCaller("mallory"), "network.delete"); err == nil {
		t.Error("non-super mallory should be denied")
	}
	if err := s.authorizeRegistry(context.Background(), "network.create"); err == nil {
		t.Error("anonymous caller should be denied under enforcement")
	}

	// Enforcement off → no-op regardless of caller.
	off := NewServer(Config{EnforceAuthorization: false})
	t.Cleanup(func() { _ = off.Stop(context.Background()) })
	if err := off.authorizeRegistry(withCaller("mallory"), "network.delete"); err != nil {
		t.Errorf("enforcement off should allow: %v", err)
	}
}

// TestCreateNetwork_RejectsReservedName pins that the archive store's reserved
// name can't be created as a network (§15, symmetric with the discovery skip).
func TestCreateNetwork_RejectsReservedName(t *testing.T) {
	s := NewServer(Config{NetworksBase: t.TempDir()})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	err := s.CreateNetwork(spec.ArchiveDirName, "")
	var ve *newtron.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("CreateNetwork(%q) err = %v, want *newtron.ValidationError", spec.ArchiveDirName, err)
	}
}
