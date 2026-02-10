package newtlab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseUname(t *testing.T) {
	tests := []struct {
		input   string
		goos    string
		goarch  string
		wantErr bool
	}{
		{"Linux x86_64\n", "linux", "amd64", false},
		{"Linux aarch64\n", "linux", "arm64", false},
		{"Darwin arm64\n", "darwin", "arm64", false},
		{"Darwin x86_64\n", "darwin", "amd64", false},
		{"Linux amd64\n", "linux", "amd64", false},
		{"FreeBSD amd64\n", "", "", true},      // unsupported OS
		{"Linux mips\n", "", "", true},          // unsupported arch
		{"badformat\n", "", "", true},           // wrong field count
		{"", "", "", true},                      // empty
	}

	for _, tt := range tests {
		goos, goarch, err := parseUname(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseUname(%q) = (%q, %q, nil), want error", tt.input, goos, goarch)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUname(%q) error: %v", tt.input, err)
			continue
		}
		if goos != tt.goos || goarch != tt.goarch {
			t.Errorf("parseUname(%q) = (%q, %q), want (%q, %q)", tt.input, goos, goarch, tt.goos, tt.goarch)
		}
	}
}

func TestNewtlinkBinaryName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "newtlink-linux-amd64"},
		{"linux", "arm64", "newtlink-linux-arm64"},
		{"darwin", "arm64", "newtlink-darwin-arm64"},
		{"darwin", "amd64", "newtlink-darwin-amd64"},
	}

	for _, tt := range tests {
		got := newtlinkBinaryName(tt.goos, tt.goarch)
		if got != tt.want {
			t.Errorf("newtlinkBinaryName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestFindNewtlinkBinary_EnvOverride(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake binary
	binPath := filepath.Join(tmpDir, "newtlink-linux-amd64")
	if err := os.WriteFile(binPath, []byte("fake"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("NEWTLAB_BIN_DIR", tmpDir)

	got, err := findNewtlinkBinary("linux", "amd64")
	if err != nil {
		t.Fatalf("findNewtlinkBinary error: %v", err)
	}
	if got != binPath {
		t.Errorf("findNewtlinkBinary = %q, want %q", got, binPath)
	}
}

func TestFindNewtlinkBinary_NotFound(t *testing.T) {
	// Clear env var and use a temp HOME so nothing is found
	t.Setenv("NEWTLAB_BIN_DIR", "")
	t.Setenv("HOME", t.TempDir())

	_, err := findNewtlinkBinary("linux", "amd64")
	if err == nil {
		t.Error("findNewtlinkBinary should error for missing binary")
	}
}

func TestRunBridgeFromFile_BadPath(t *testing.T) {
	err := RunBridgeFromFile("/nonexistent/path/bridge.json")
	if err == nil {
		t.Error("RunBridgeFromFile should error for nonexistent config")
	}
}
