package newtlab

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// QEMUCommand builds a QEMU invocation for a single node.
type QEMUCommand struct {
	Node     *NodeConfig
	StateDir string // lab state directory
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

	if kvmAvailable() {
		args = append(args, "-enable-kvm")
	}

	// Disk
	overlayPath := filepath.Join(q.StateDir, "disks", name+".qcow2")
	args = append(args, "-drive", fmt.Sprintf("file=%s,if=virtio,format=qcow2", overlayPath))

	// Display
	args = append(args, "-nographic")

	// Serial console
	args = append(args, "-serial", fmt.Sprintf("tcp::%d,server,nowait", q.Node.ConsolePort))

	// Monitor socket
	monSocket := filepath.Join(q.StateDir, "qemu", name+".mon")
	args = append(args, "-monitor", fmt.Sprintf("unix:%s,server,nowait", monSocket))

	// PID file
	pidFile := filepath.Join(q.StateDir, "qemu", name+".pid")
	args = append(args, "-pidfile", pidFile)

	// Management NIC (NIC 0): user-mode networking with SSH port forward
	args = append(args,
		"-netdev", fmt.Sprintf("user,id=mgmt,hostfwd=tcp::%d-:22", q.Node.SSHPort),
		"-device", fmt.Sprintf("%s,netdev=mgmt", q.Node.NICDriver),
	)

	// Data NICs (NIC 1..N)
	for _, nic := range q.Node.NICs {
		if nic.Index == 0 {
			continue // skip mgmt, already handled
		}
		var netdev string
		if nic.Listen {
			netdev = fmt.Sprintf("socket,id=%s,listen=:%d", nic.NetdevID, nic.LinkPort)
		} else {
			netdev = fmt.Sprintf("socket,id=%s,connect=%s:%d", nic.NetdevID, nic.RemoteIP, nic.LinkPort)
		}
		args = append(args,
			"-netdev", netdev,
			"-device", fmt.Sprintf("%s,netdev=%s", q.Node.NICDriver, nic.NetdevID),
		)
	}

	return exec.Command("qemu-system-x86_64", args...)
}

// StartNode launches the QEMU process for a node.
// Redirects stdout/stderr to logs/<name>.log.
// Returns PID after process is started (does not wait for boot).
func StartNode(node *NodeConfig, stateDir string) (int, error) {
	qemu := &QEMUCommand{Node: node, StateDir: stateDir}
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

// StopNode sends SIGTERM to the QEMU process, then SIGKILL after 10s.
func StopNode(pid int) error {
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
		if !IsRunning(pid) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Force kill
	process.Signal(syscall.SIGKILL)
	return nil
}

// IsRunning checks if a QEMU process is alive by PID.
func IsRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks if process exists without sending a signal
	err = process.Signal(syscall.Signal(0))
	return err == nil
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

// probePort attempts net.Listen on the given port to check availability.
// Immediately closes the listener. Returns error if the port is in use.
func probePort(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("port %d already in use", port)
	}
	ln.Close()
	return nil
}
