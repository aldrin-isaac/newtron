package newtlab

import "testing"

// TestCmdlineBelongsToLab pins the two subtle correctness points of the #444
// teardown sweep: the trailing-separator guard against sibling-prefix labs, and
// the binary check that spares unrelated processes that merely name the path.
func TestCmdlineBelongsToLab(t *testing.T) {
	const dir = "/home/u/.newtlab/labs/2node-vs"
	cases := []struct {
		name    string
		cmdline string
		want    bool
	}{
		{"qemu disk under dir", "qemu-system-x86_64 -drive file=" + dir + "/disks/switch1.qcow2,if=virtio", true},
		{"qemu pidfile under dir", "qemu-system-x86_64 -pidfile " + dir + "/qemu/switch1.pid", true},
		{"newtlink bridge config", "/opt/bin/newtlink " + dir + "/bridge.json", true},
		{"sibling prefix lab is NOT a match", "qemu-system-x86_64 -pidfile /home/u/.newtlab/labs/2node-vs-service/qemu/s1.pid", false},
		{"path but wrong binary (a shell/grep)", "/bin/bash -c pgrep -f " + dir + "/bridge.json", false},
		{"binary but different lab", "qemu-system-x86_64 -pidfile /home/u/.newtlab/labs/other/qemu/s1.pid", false},
		{"dir named exactly, no trailing child", "qemu-system-x86_64 --note " + dir, false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cmdlineBelongsToLab(c.cmdline, dir); got != c.want {
				t.Errorf("cmdlineBelongsToLab(%q) = %v, want %v", c.cmdline, got, c.want)
			}
		})
	}
}
