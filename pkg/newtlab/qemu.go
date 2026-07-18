package newtlab

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// QEMUCommand builds a QEMU invocation for a single node.
type QEMUCommand struct {
	Node     *NodeConfig
	StateDir string // lab state directory (absolute for local, "." for remote)
	KVM      bool   // enable KVM acceleration
}

// Build returns an exec.Cmd ready to start the QEMU process.
func (q *QEMUCommand) Build() *exec.Cmd {
	name := q.Node.Name
	args := []string{
		"-m", fmt.Sprintf("%d", q.Node.Memory),
		"-smp", fmt.Sprintf("%d", q.Node.CPUs),
	}

	// CPU: host with optional features, or KVM auto-detect
	if q.Node.CPUFeatures != "" {
		args = append(args, "-cpu", "host,"+q.Node.CPUFeatures)
	} else {
		args = append(args, "-cpu", "host")
	}

	if q.KVM {
		args = append(args, "-enable-kvm")
	}

	// Disk
	overlayPath := filepath.Join(q.StateDir, "disks", name+".qcow2")
	args = append(args, "-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", overlayPath))

	// Display: use -display none (not -nographic, which adds an implicit -serial mon:stdio
	// that conflicts with our explicit serial below and pushes console to ttyS1)
	args = append(args, "-display", "none")

	// Serial console: ttyS0 → TCP so we can connect via telnet/netcat
	args = append(args, "-serial", fmt.Sprintf("tcp::%d,server,nowait", q.Node.ConsolePort))

	// Boot from hard drive (not NIC PXE ROM)
	args = append(args, "-boot", "c")

	// Monitor socket
	monSocket := filepath.Join(q.StateDir, "qemu", name+".mon")
	args = append(args, "-monitor", fmt.Sprintf("unix:%s,server,nowait", monSocket))

	// PID file
	pidFile := filepath.Join(q.StateDir, "qemu", name+".pid")
	args = append(args, "-pidfile", pidFile)

	// Management NIC (NIC 0): user-mode networking with SSH port forward
	// romfile= disables PXE boot ROM so QEMU boots from disk
	// Find mgmt NIC to get its MAC
	mgmtMAC := ""
	for _, nic := range q.Node.NICs {
		if nic.Index == 0 {
			mgmtMAC = nic.MAC
			break
		}
	}
	args = append(args,
		"-netdev", fmt.Sprintf("user,id=mgmt,hostfwd=tcp::%d-:22", q.Node.SSHPort),
		"-device", fmt.Sprintf("%s,netdev=mgmt,mac=%s,romfile=", q.Node.NICDriver, mgmtMAC),
	)

	// Data NICs (NIC 1..N): all connect outbound to bridge workers.
	// Sort by Index so kernel ethN matches NIC index N — QEMU enumerates
	// NICs in PCI order, so the Nth data device in the command becomes
	// kernel eth(N), which must equal NIC index N for TC mirred to work.
	sortedNICs := make([]NICConfig, len(q.Node.NICs))
	copy(sortedNICs, q.Node.NICs)
	sort.Slice(sortedNICs, func(i, j int) bool { return sortedNICs[i].Index < sortedNICs[j].Index })
	for _, nic := range sortedNICs {
		if nic.Index == 0 {
			continue // skip mgmt, already handled
		}
		if nic.ConnectAddr == "" {
			// Filler NIC (normalizeNodeNICs): occupies a position so the
			// guest's Nth data NIC matches nic_index N; carries no traffic —
			// the VM-level equivalent of an unwired front-panel port.
			args = append(args,
				"-netdev", fmt.Sprintf("user,id=%s,restrict=on", nic.NetdevID),
				"-device", fmt.Sprintf("%s,netdev=%s,mac=%s,romfile=", q.Node.NICDriver, nic.NetdevID, nic.MAC),
			)
			continue
		}
		args = append(args,
			"-netdev", fmt.Sprintf("socket,id=%s,connect=%s", nic.NetdevID, nic.ConnectAddr),
			"-device", fmt.Sprintf("%s,netdev=%s,mac=%s,romfile=", q.Node.NICDriver, nic.NetdevID, nic.MAC),
		)
	}

	return exec.Command("qemu-system-x86_64", args...)
}

// StartNode launches the QEMU process for a node.
// If hostIP is non-empty, launches on the remote host via SSH.
// Otherwise launches locally, redirecting stdout/stderr to logs/<name>.log.
// Returns PID after process is started (does not wait for boot).
func StartNode(node *NodeConfig, stateDir, hostIP string) (int, error) {
	if hostIP != "" {
		labName := filepath.Base(stateDir)
		return StartNodeRemote(node, labName, hostIP)
	}
	return startNodeLocal(node, stateDir)
}

// startNodeLocal launches QEMU on the local host.
func startNodeLocal(node *NodeConfig, stateDir string) (int, error) {
	qemu := &QEMUCommand{Node: node, StateDir: stateDir, KVM: kvmAvailable()}
	cmd := qemu.Build()

	logPath := filepath.Join(stateDir, "logs", node.Name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return 0, fmt.Errorf("newtlab: create log %s: %w", logPath, err)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Detach from parent process group so QEMU survives if newtlab exits
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("newtlab: start node %s: %w", node.Name, err)
	}

	pid := cmd.Process.Pid

	// Release the process so it doesn't become a zombie
	go func() {
		cmd.Wait()
		logFile.Close()
	}()

	return pid, nil
}

// StartNodeRemote launches QEMU on a remote host via SSH.
// It builds the QEMU command with relative paths, then executes it remotely
// after cd-ing to the lab state directory (so ~/ expands correctly).
// Returns the remote PID.
func StartNodeRemote(node *NodeConfig, labName, hostIP string) (int, error) {
	// Build QEMU command with relative paths from lab state dir
	qemu := &QEMUCommand{Node: node, StateDir: ".", KVM: true}
	localCmd := qemu.Build()

	// Build the remote command: cd to state dir, then nohup QEMU
	qemuArgs := append([]string{localCmd.Path}, localCmd.Args[1:]...)
	remoteDir := shellQuote(fmt.Sprintf("~/.newtlab/labs/%s", labName))
	remoteCmd := fmt.Sprintf("cd %s && nohup %s > /dev/null 2>&1 & echo $!",
		remoteDir, strings.Join(quoteArgs(qemuArgs), " "))

	cmd := sshCommand(hostIP, remoteCmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("newtlab: start node %s on %s: %w", node.Name, hostIP, err)
	}

	pidStr := strings.TrimSpace(stdout.String())
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("newtlab: parse remote PID for %s: %w (output: %q)", node.Name, err, pidStr)
	}

	return pid, nil
}

// StopNode sends SIGTERM to the QEMU process, then SIGKILL after 10s.
// If hostIP is non-empty, kills the process on the remote host via SSH.
func StopNode(pid int, hostIP string) error {
	if hostIP != "" {
		return StopNodeRemote(pid, hostIP)
	}
	return stopNodeLocal(pid)
}

// stopNodeLocal kills a local QEMU process.
func stopNodeLocal(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("newtlab: find process %d: %w", pid, err)
	}

	// Try SIGTERM first
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Process may already be dead
		return nil
	}

	// Wait up to 10s for graceful shutdown
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !IsRunning(pid, "") {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill
	process.Signal(syscall.SIGKILL)
	return nil
}

// StopNodeRemote kills a QEMU process on a remote host via SSH.
func StopNodeRemote(pid int, hostIP string) error {
	// Try SIGTERM first
	cmd := sshCommand(hostIP, fmt.Sprintf("kill %d 2>/dev/null; exit 0", pid))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("newtlab: kill remote pid %d on %s: %w", pid, hostIP, err)
	}

	// Wait up to 10s for graceful shutdown
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !IsRunning(pid, hostIP) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill
	sshCommand(hostIP, fmt.Sprintf("kill -9 %d 2>/dev/null; exit 0", pid)).Run()
	return nil
}

// IsRunning checks if a QEMU process is alive by PID.
// If hostIP is non-empty, checks on the remote host via SSH.
func IsRunning(pid int, hostIP string) bool {
	if hostIP != "" {
		return IsRunningRemote(pid, hostIP)
	}
	return isRunningLocal(pid)
}

// isRunningLocal checks if a local process is alive.
func isRunningLocal(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without sending a signal
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// IsRunningRemote checks if a process is alive on a remote host via SSH.
func IsRunningRemote(pid int, hostIP string) bool {
	cmd := sshCommand(hostIP, fmt.Sprintf("kill -0 %d 2>/dev/null", pid))
	return cmd.Run() == nil
}

// processCmdline returns the argv of a local process (NULs turned to spaces),
// or "" if the process is gone or unreadable.
func processCmdline(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(string(b), "\x00", " ")
}

// processBelongsToLab reports whether pid is a live qemu/newtlink process this
// lab launched: its argv names the qemu/newtlink binary AND references a path
// under the lab's state dir (every VM carries -pidfile/-drive under it; the
// bridge carries <stateDir>/bridge.json). The trailing separator prevents a
// prefix collision (`2node-vs` must not match `2node-vs-service`). This is the
// identity check that makes a kill safe against PID reuse and lets Destroy
// sweep orphans the state ledger lost (issue #444).
func processBelongsToLab(pid int, stateDir string) bool {
	return cmdlineBelongsToLab(processCmdline(pid), stateDir)
}

// cmdlineBelongsToLab is the pure predicate behind processBelongsToLab: the
// argv must name the qemu/newtlink binary AND reference a path UNDER stateDir.
// The trailing separator is load-bearing — it stops `2node-vs` from matching a
// sibling `2node-vs-service`; the binary check stops an unrelated shell that
// merely mentions the path (a grep, this very audit) from being reaped.
func cmdlineBelongsToLab(cmdline, stateDir string) bool {
	if cmdline == "" || !strings.Contains(cmdline, stateDir+string(os.PathSeparator)) {
		return false
	}
	return strings.Contains(cmdline, "qemu-system") || strings.Contains(cmdline, "newtlink")
}

// findLabProcesses scans /proc for every live qemu/newtlink process that
// belongs to the given lab state dir — tracked or orphaned. Local only.
func findLabProcesses(stateDir string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid dir
		}
		if processBelongsToLab(pid, stateDir) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// killLabProcess SIGTERMs, then (after a grace period) SIGKILLs pid — but only
// while it still belongs to the lab, so a recycled PID is never signalled.
// Returns true once the pid is gone.
func killLabProcess(pid int, stateDir string) bool {
	if !processBelongsToLab(pid, stateDir) {
		return true // not ours (reused or already gone)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !isRunningLocal(pid) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	proc.Signal(syscall.SIGKILL)
	for i := 0; i < 8; i++ {
		if !isRunningLocal(pid) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return !isRunningLocal(pid)
}

// kvmAvailable returns true if /dev/kvm exists and is writable.
func kvmAvailable() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

