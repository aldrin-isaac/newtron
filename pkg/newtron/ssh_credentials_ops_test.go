package newtron

import "testing"

// TestMaskSSHPass pins the read-side masking contract of ShowSSHCredentials: a
// ${secret:KEY} reference is a pointer (shown so a UI/auditor sees which key is
// referenced), a plaintext value is never echoed (replaced with a placeholder),
// and empty stays empty (nothing authored at this scope).
func TestMaskSSHPass(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""}, // nothing authored
		{"${secret:switch1_ssh_pass}", "${secret:switch1_ssh_pass}"}, // ref preserved
		{"literalPassword", "***redacted***"},                        // plaintext masked
		{"admin123", "***redacted***"},                               // plaintext masked
	}
	for _, c := range cases {
		if got := maskSSHPass(c.in); got != c.want {
			t.Errorf("maskSSHPass(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
