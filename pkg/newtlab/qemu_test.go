package newtlab

import "testing"

// TestBelongsToLab pins the identity check behind the #444 teardown sweep. The
// executable-vs-substring distinction is the one that matters: an early version
// substring-matched "qemu-system"/"newtlink" anywhere in the command line and
// reaped the shell that was verifying the teardown (its args named the binary).
func TestBelongsToLab(t *testing.T) {
	const dir = "/home/u/.newtlab/labs/2node-vs"
	cases := []struct {
		name    string
		exeBase string
		args    string
		want    bool
	}{
		{"qemu disk under dir", "qemu-system-x86_64", "qemu-system-x86_64 -drive file=" + dir + "/disks/switch1.qcow2", true},
		{"qemu pidfile under dir", "qemu-system-x86_64", "qemu-system-x86_64 -pidfile " + dir + "/qemu/switch1.pid", true},
		{"newtlink bridge config", "newtlink", "/opt/bin/newtlink " + dir + "/bridge.json", true},

		// The bug that bit the live audit: a shell whose ARGS name the binary
		// and the dir, but whose EXECUTABLE is not qemu/newtlink.
		{"shell mentioning qemu-system + dir", "bash", "bash -c pgrep -f qemu-system-x86_64 " + dir + "/bridge.json", false},
		{"grep for newtlink under dir", "grep", "grep newtlink " + dir + "/x", false},

		{"sibling prefix lab is NOT a match", "qemu-system-x86_64", "qemu-system-x86_64 -pidfile /home/u/.newtlab/labs/2node-vs-service/qemu/s1.pid", false},
		{"qemu but different lab", "qemu-system-x86_64", "qemu-system-x86_64 -pidfile /home/u/.newtlab/labs/other/qemu/s1.pid", false},
		{"qemu but dir named exactly, no trailing child", "qemu-system-x86_64", "qemu-system-x86_64 --note " + dir, false},
		{"exe empty (process gone)", "", "anything " + dir + "/x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := belongsToLab(c.exeBase, c.args, dir); got != c.want {
				t.Errorf("belongsToLab(%q, %q) = %v, want %v", c.exeBase, c.args, got, c.want)
			}
		})
	}
}
