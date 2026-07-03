package api

import (
	"context"
	"errors"
	"net/http"
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

// TestDeleteNetwork_UnregistersAndArchivesRegistered pins that delete OWNS its
// teardown: a still-registered network is unregistered (serving torn down) and
// archived in one call — no separate unregister step required.
func TestDeleteNetwork_UnregistersAndArchivesRegistered(t *testing.T) {
	base := t.TempDir()
	dir := scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{NetworksBase: base})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if err := s.RegisterNetwork("mynet", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}

	dst, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp)
	if err != nil {
		t.Fatalf("DeleteNetwork on a registered network: %v", err)
	}
	if s.getNetwork("mynet") != nil {
		t.Error("network still registered after delete — teardown didn't run")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("source dir still present after archive: %v", statErr)
	}
	if _, statErr := os.Stat(dst); statErr != nil {
		t.Errorf("archived dir missing: %v", statErr)
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

// TestDeleteNetwork_LabGuardRunsBeforeTeardown pins the key property of the
// re-bundled delete: a lab-guard failure changes NOTHING — the network stays
// registered and on disk, never torn down for a delete that didn't happen. force
// then bypasses the guard and does the teardown+archive.
func TestDeleteNetwork_LabGuardRunsBeforeTeardown(t *testing.T) {
	base := t.TempDir()
	dir := scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{
		NetworksBase: base,
		LabDeployed:  func(context.Context, string) (bool, error) { return true, nil },
	})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if err := s.RegisterNetwork("mynet", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}

	_, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp)
	var ce *util.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *util.ConflictError (lab deployed)", err)
	}
	// Guard ran BEFORE teardown: still in service, dir intact.
	if s.getNetwork("mynet") == nil {
		t.Error("network unregistered by a delete that failed the lab guard")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("dir moved by a delete that failed the lab guard: %v", statErr)
	}

	// force bypasses the guard → teardown + archive.
	if _, err := s.DeleteNetwork(context.Background(), "mynet", true, testStamp); err != nil {
		t.Errorf("force delete with lab deployed = %v, want nil", err)
	}
	if s.getNetwork("mynet") != nil {
		t.Error("network still registered after force delete")
	}
}

// TestDeleteNetwork_LabCheckErrorFailsClosed pins that an error probing lab state
// refuses the delete AND leaves the network fully in service (never torn down on
// uncertainty).
func TestDeleteNetwork_LabCheckErrorFailsClosed(t *testing.T) {
	base := t.TempDir()
	dir := scaffoldNetworkDir(t, base, "mynet")
	s := NewServer(Config{
		NetworksBase: base,
		LabDeployed:  func(context.Context, string) (bool, error) { return false, errors.New("newtlab unreachable") },
	})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if err := s.RegisterNetwork("mynet", dir); err != nil {
		t.Fatalf("RegisterNetwork: %v", err)
	}

	if _, err := s.DeleteNetwork(context.Background(), "mynet", false, testStamp); err == nil {
		t.Error("lab-check error should refuse the delete; got nil")
	}
	if s.getNetwork("mynet") == nil {
		t.Error("network unregistered despite a lab-check error")
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("dir moved despite a lab-check error: %v", statErr)
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

// TestHandleCreateNetwork_GatesOnlyGenuineCreation pins that the create gate
// fires ONLY when scaffolding a brand-new network — registering an existing
// on-disk network (the serving layer, the path bin/newtlab deploy takes) stays
// open to a non-super operator. Guards the regression where gating all of
// POST /networks 403'd every non-super deploy.
func TestHandleCreateNetwork_GatesOnlyGenuineCreation(t *testing.T) {
	base := t.TempDir()
	s := NewServer(Config{
		AuditCallerHeader:    "X-Newtron-Caller",
		EnforceAuthorization: true,
		GlobalSuperUsers:     []string{"ron"},
		NetworksBase:         base,
	})
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	// (1) Non-super scaffolding a brand-new network → 403.
	if w := postAs(t, s, "mallory", "/newtron/v1/networks", map[string]string{"id": "brandnew"}); w.Code != http.StatusForbidden {
		t.Errorf("non-super create-new = %d, want 403", w.Code)
	}

	// (2) Non-super registering an EXISTING on-disk network → allowed (serving layer).
	scaffoldNetworkDir(t, base, "existing")
	if w := postAs(t, s, "mallory", "/newtron/v1/networks", map[string]string{"id": "existing"}); w.Code == http.StatusForbidden {
		t.Errorf("non-super register-existing = 403, want allowed; body=%s", w.Body.String())
	}

	// (3) Global super-user CAN scaffold a new network.
	if w := postAs(t, s, "ron", "/newtron/v1/networks", map[string]string{"id": "supernet"}); w.Code == http.StatusForbidden {
		t.Errorf("super-user create-new = 403, want created; body=%s", w.Body.String())
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
