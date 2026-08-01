package newtlab

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTeardownRefusesUndeterminableScope pins the guard: if neither StateDir
// nor NetworkID identifies a lab, teardown must abort rather than sweep an
// empty scope. An empty scope matches no process, so the reap does nothing and
// the survivor gate passes vacuously — the ledger would be erased with the VMs
// still running and no record of their PIDs.
func TestTeardownRefusesUndeterminableScope(t *testing.T) {
	if _, err := (&Lab{}).sweepScope(); err == nil {
		t.Error("sweepScope on a Lab with no StateDir and no NetworkID returned no error — teardown would sweep an unbounded scope")
	}
}

// TestSweepScopeDerivedForBareLab pins the fix for the orphaning bug: the CLI
// verbs and the ByName helpers build &Lab{NetworkID: name} without StateDir,
// and the sweep must still resolve to that lab's own directory. Reading
// l.StateDir directly yielded "" for those callers, which matched nothing:
// destroy reported success while leaving the lab's VMs running forever.
func TestSweepScopeDerivedForBareLab(t *testing.T) {
	scope, err := (&Lab{NetworkID: "2node-vs"}).sweepScope()
	if err != nil {
		t.Fatalf("sweepScope: %v", err)
	}
	if want := LabDir("2node-vs"); scope != want {
		t.Errorf("scope = %q, want %q", scope, want)
	}
	if scope == "" || strings.HasSuffix(filepath.Clean(scope), "labs") {
		t.Errorf("scope %q is not lab-specific — it would match every lab on the host", scope)
	}
}

// TestTeardownReportsSurvivingProcess is the end-to-end guard: a teardown that
// cannot kill one of the lab's processes must report failure AND retain the
// ledger, so the operator can re-run destroy. Before the scope fix this test's
// process was invisible to the sweep, so teardown reported success and removed
// the state — orphaning the process with nothing left pointing at it.
//
// The stand-in is a `sleep` renamed to qemu-system-x86_64 so it satisfies
// belongsToLab (executable prefix + an argument under the lab dir) without
// needing a real VM.
func TestTeardownReportsSurvivingProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetHomeDir()

	const netID = "scope-test"
	labDir := LabDir(netID)
	if err := os.MkdirAll(filepath.Join(labDir, "qemu"), 0o755); err != nil {
		t.Fatalf("mkdir lab dir: %v", err)
	}
	if err := SaveState(&LabState{NetworkID: netID, Nodes: map[string]*NodeState{}}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// A process the sweep must see: executable named like qemu (belongsToLab
	// checks the exe, not the cmdline) and an argument under the lab dir. `cat`
	// on a FIFO nobody writes to blocks forever, which keeps the process alive
	// while holding that path — `sleep` would reject a path argument and exit,
	// leaving a zombie whose /proc entries read empty.
	fake := filepath.Join(home, "qemu-system-x86_64")
	if err := copyFile("/bin/cat", fake); err != nil {
		t.Skipf("cannot stage fake qemu: %v", err)
	}
	fifo := filepath.Join(labDir, "qemu", "switch1.mon")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}
	cmd := exec.Command(fake, fifo)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake qemu: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	// Give /proc a moment to expose the new process's cmdline.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(findLabProcesses(labDir)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(findLabProcesses(labDir)) == 0 {
		t.Fatal("the sweep cannot see the staged process — the test cannot prove anything")
	}

	// Bare literal, exactly as `newtlab destroy` and the ByName helpers build it.
	lab := &Lab{NetworkID: netID}
	err := lab.teardown(&LabState{NetworkID: netID, Nodes: map[string]*NodeState{}}, false)

	// killLabProcess SIGKILLs it, so teardown legitimately succeeds — what must
	// NOT happen is teardown reporting success while the process is still alive.
	if err == nil && isRunningLocal(cmd.Process.Pid) {
		t.Error("teardown reported success while the lab's process is still running — the ledger was erased and the process orphaned")
	}
	if err != nil && !isRunningLocal(cmd.Process.Pid) {
		t.Errorf("teardown reported failure but the process is gone: %v", err)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

var _ = context.Background
