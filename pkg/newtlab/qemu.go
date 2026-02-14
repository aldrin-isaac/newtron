package newtlab

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	args = append(args,
		"-netdev", fmt.Sprintf("user,id=mgmt,hostfwd=tcp::%d-:22", q.Node.SSHPort),
		"-device", fmt.Sprintf("%s,netdev=mgmt,romfile=", q.Node.NICDriver),
	)

	// Data NICs (NIC 1..N): all connect outbound to bridge workers
	for _, nic := range q.Node.NICs {
		if nic.Index == 0 {
			continue // skip mgmt, already handled
		}
		args = append(args,
			"-netdev", fmt.Sprintf("socket,id=%s,connect=%s", nic.NetdevID, nic.ConnectAddr),
			"-device", fmt.Sprintf("%s,netdev=%s,romfile=", q.Node.NICDriver, nic.NetdevID),
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

// kvmAvailable returns true if /dev/kvm exists and is writable.
func kvmAvailable() bool {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

