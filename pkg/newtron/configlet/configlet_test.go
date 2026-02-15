package configlet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfiglet(t *testing.T) {
	dir := t.TempDir()

	// Write a valid configlet JSON
	data := `{
		"name": "test-baseline",
		"description": "Test configlet",
		"version": "1.0",
		"config_db": {
			"DEVICE_METADATA": {
				"localhost": {
					"hostname": "{{hostname}}",
					"platform": "x86_64-kvm"
				}
			}
		},
		"variables": ["hostname"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "test-baseline.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadConfiglet(dir, "test-baseline")
	if err != nil {
		t.Fatalf("LoadConfiglet error: %v", err)
	}

	if c.Name != "test-baseline" {
		t.Errorf("Name = %q, want %q", c.Name, "test-baseline")
	}
	if c.Description != "Test configlet" {
		t.Errorf("Description = %q, want %q", c.Description, "Test configlet")
	}
	if c.Version != "1.0" {
		t.Errorf("Version = %q, want %q", c.Version, "1.0")
	}
	if len(c.Variables) != 1 || c.Variables[0] != "hostname" {
		t.Errorf("Variables = %v, want [hostname]", c.Variables)
	}
	if len(c.ConfigDB) != 1 {
		t.Errorf("ConfigDB has %d tables, want 1", len(c.ConfigDB))
	}
}

func TestLoadConfiglet_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadConfiglet(dir, "nonexistent")
	if err == nil {
		t.Error("expected error for missing configlet")
	}
}

func TestLoadConfiglet_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfiglet(dir, "bad")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestListConfiglets(t *testing.T) {
	dir := t.TempDir()

	// Create some configlet files and a non-JSON file
	for _, name := range []string{"alpha.json", "beta.json", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	names, err := ListConfiglets(dir)
	if err != nil {
		t.Fatalf("ListConfiglets error: %v", err)
	}

	if len(names) != 2 {
		t.Fatalf("ListConfiglets returned %d names, want 2: %v", len(names), names)
	}

	// Check names (without .json suffix)
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("expected alpha and beta, got %v", names)
	}
}

func TestListConfiglets_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	names, err := ListConfiglets(dir)
	if err != nil {
		t.Fatalf("ListConfiglets error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestListConfiglets_NonexistentDir(t *testing.T) {
	_, err := ListConfiglets("/nonexistent/path")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestResolveVariables(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		vars   map[string]string
		want   string
	}{
		{
			name:  "single variable",
			input: "hostname is {{hostname}}",
			vars:  map[string]string{"hostname": "leaf1"},
			want:  "hostname is leaf1",
		},
		{
			name:  "multiple variables",
			input: "{{hostname}} in {{region}}",
			vars:  map[string]string{"hostname": "leaf1", "region": "us-east"},
			want:  "leaf1 in us-east",
		},
		{
			name:  "missing variable left as-is",
			input: "{{hostname}} in {{region}}",
			vars:  map[string]string{"hostname": "leaf1"},
			want:  "leaf1 in {{region}}",
		},
		{
			name:  "no variables",
			input: "no placeholders here",
			vars:  map[string]string{"hostname": "leaf1"},
			want:  "no placeholders here",
		},
		{
			name:  "empty vars map",
			input: "{{hostname}}",
			vars:  map[string]string{},
			want:  "{{hostname}}",
		},
		{
			name:  "repeated variable",
			input: "{{x}} and {{x}}",
			vars:  map[string]string{"x": "val"},
			want:  "val and val",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveVariables(tt.input, tt.vars)
			if got != tt.want {
				t.Errorf("ResolveVariables(%q, %v) = %q, want %q", tt.input, tt.vars, got, tt.want)
			}
		})
	}
}

func TestResolveConfiglet(t *testing.T) {
	c := &Configlet{
		ConfigDB: map[string]map[string]interface{}{
			"DEVICE_METADATA": {
				"localhost": map[string]interface{}{
					"hostname": "{{hostname}}",
					"platform": "x86_64",
				},
			},
			"PORT": {
				"{{intf}}": map[string]interface{}{
					"speed": "100000",
				},
			},
		},
	}

	vars := map[string]string{
		"hostname": "leaf1-ny",
		"intf":     "Ethernet0",
	}

	result := ResolveConfiglet(c, vars)

	// Check DEVICE_METADATA
	if result["DEVICE_METADATA"]["localhost"]["hostname"] != "leaf1-ny" {
		t.Errorf("hostname = %q, want %q", result["DEVICE_METADATA"]["localhost"]["hostname"], "leaf1-ny")
	}
	if result["DEVICE_METADATA"]["localhost"]["platform"] != "x86_64" {
		t.Errorf("platform = %q, want %q", result["DEVICE_METADATA"]["localhost"]["platform"], "x86_64")
	}

	// Check PORT — key should be resolved
	if _, ok := result["PORT"]["Ethernet0"]; !ok {
		t.Error("expected resolved key 'Ethernet0' in PORT table")
	}
	if result["PORT"]["Ethernet0"]["speed"] != "100000" {
		t.Errorf("speed = %q, want %q", result["PORT"]["Ethernet0"]["speed"], "100000")
	}
}

func TestResolveConfiglet_MapStringString(t *testing.T) {
	// Test with map[string]string value type (after JSON round-trip this becomes interface{})
	c := &Configlet{
		ConfigDB: map[string]map[string]interface{}{
			"TABLE": {
				"key": map[string]string{
					"field": "{{val}}",
				},
			},
		},
	}

	result := ResolveConfiglet(c, map[string]string{"val": "resolved"})
	if result["TABLE"]["key"]["field"] != "resolved" {
		t.Errorf("field = %q, want %q", result["TABLE"]["key"]["field"], "resolved")
	}
}

