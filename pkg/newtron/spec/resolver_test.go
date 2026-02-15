package spec

import (
	"reflect"
	"testing"
)

func TestResolver_ResolveString(t *testing.T) {
	aliases := map[string]string{
		"asnum":     "65000",
		"region":    "amer",
		"site-name": "ny-dc1",
	}
	r := NewResolver(aliases, nil)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "braces syntax",
			input: "AS${asnum}",
			want:  "AS65000",
		},
		{
			name:  "dollar syntax",
			input: "AS$asnum",
			want:  "AS65000",
		},
		{
			name:  "multiple aliases",
			input: "${region}-${site-name}",
			want:  "amer-ny-dc1",
		},
		{
			name:  "no alias",
			input: "plain text",
			want:  "plain text",
		},
		{
			name:  "unknown alias preserved",
			input: "${unknown}",
			want:  "${unknown}",
		},
		{
			name:  "mixed known and unknown",
			input: "${region}-${unknown}",
			want:  "amer-${unknown}",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ResolveString(tt.input)
			if got != tt.want {
				t.Errorf("ResolveString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolver_ResolvePrefixList(t *testing.T) {
	prefixLists := map[string][]string{
		"rfc1918": {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		"bogons":  {"0.0.0.0/8", "127.0.0.0/8"},
	}
	r := NewResolver(nil, prefixLists)

	t.Run("existing list", func(t *testing.T) {
		list, ok := r.ResolvePrefixList("rfc1918")
		if !ok {
			t.Error("Should find rfc1918")
		}
		if len(list) != 3 {
			t.Errorf("Expected 3 entries, got %d", len(list))
		}
	})

	t.Run("non-existing list", func(t *testing.T) {
		_, ok := r.ResolvePrefixList("nonexistent")
		if ok {
			t.Error("Should not find nonexistent list")
		}
	})
}

func TestResolver_ExpandPrefixLists(t *testing.T) {
	prefixLists := map[string][]string{
		"rfc1918": {"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		"bogons":  {"0.0.0.0/8", "127.0.0.0/8"},
	}
	r := NewResolver(nil, prefixLists)

	tests := []struct {
		name    string
		entries []string
		want    []string
	}{
		{
			name:    "expand single list",
			entries: []string{"@rfc1918"},
			want:    []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
		},
		{
			name:    "mixed entries",
			entries: []string{"1.1.1.1/32", "@bogons", "8.8.8.8/32"},
			want:    []string{"1.1.1.1/32", "0.0.0.0/8", "127.0.0.0/8", "8.8.8.8/32"},
		},
		{
			name:    "unknown list preserved",
			entries: []string{"@unknown"},
			want:    []string{"@unknown"},
		},
		{
			name:    "no expansion needed",
			entries: []string{"10.0.0.0/8", "20.0.0.0/8"},
			want:    []string{"10.0.0.0/8", "20.0.0.0/8"},
		},
		{
			name:    "empty entries",
			entries: []string{},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.ExpandPrefixLists(tt.entries)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExpandPrefixLists(%v) = %v, want %v", tt.entries, got, tt.want)
			}
		})
	}
}

func TestResolver_ResolveAllStrings(t *testing.T) {
	aliases := map[string]string{
		"region": "amer",
		"asnum":  "65000",
	}
	r := NewResolver(aliases, nil)

	input := map[string]string{
		"name":        "${region}-router",
		"description": "AS${asnum} router",
		"plain":       "no aliases",
	}

	got := r.ResolveAllStrings(input)

	want := map[string]string{
		"name":        "amer-router",
		"description": "AS65000 router",
		"plain":       "no aliases",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveAllStrings() = %v, want %v", got, want)
	}
}

func TestResolver_MergeAliases(t *testing.T) {
	r := NewResolver(map[string]string{"a": "1"}, nil)
	r.MergeAliases(map[string]string{"b": "2", "a": "override"})

	if v, _ := r.GetAlias("a"); v != "override" {
		t.Errorf("Merge should override: got %q", v)
	}
	if v, _ := r.GetAlias("b"); v != "2" {
		t.Errorf("Merge should add new: got %q", v)
	}
}

func TestResolver_MergePrefixLists(t *testing.T) {
	r := NewResolver(nil, map[string][]string{"a": {"1", "2"}})
	r.MergePrefixLists(map[string][]string{"b": {"3", "4"}, "a": {"override"}})

	if list, ok := r.ResolvePrefixList("a"); !ok || len(list) != 1 || list[0] != "override" {
		t.Errorf("Merge should override: got %v", list)
	}
	if list, ok := r.ResolvePrefixList("b"); !ok || len(list) != 2 {
		t.Errorf("Merge should add new: got %v", list)
	}
}

func TestResolver_GetSetAlias(t *testing.T) {
	r := NewResolver(make(map[string]string), nil)

	// Initially not set
	if _, ok := r.GetAlias("test"); ok {
		t.Error("Should not find unset alias")
	}

	// Set and get
	r.SetAlias("test", "value")
	if v, ok := r.GetAlias("test"); !ok || v != "value" {
		t.Errorf("GetAlias() = %q, %v, want %q, true", v, ok, "value")
	}
}

func TestAliasContext(t *testing.T) {
	aliases := map[string]string{
		"base": "prefix",
	}
	r := NewResolver(aliases, nil)

	ctx := NewAliasContext(r, "leaf1-ny")

	// Device should resolve through context
	if resolved := ctx.Resolve("${device}"); resolved != "leaf1-ny" {
		t.Errorf("Device alias not set: %q", resolved)
	}

	// Parent resolver should NOT be mutated (isolation)
	if _, ok := r.GetAlias("device"); ok {
		t.Error("Parent resolver should not have device alias set")
	}

	// Add interface context
	ctx.WithInterface("Ethernet0")
	if resolved := ctx.Resolve("${interface}"); resolved != "Ethernet0" {
		t.Errorf("Interface alias not set: %q", resolved)
	}

	// Add service context
	ctx.WithService("customer-edge")
	if resolved := ctx.Resolve("${service}"); resolved != "customer-edge" {
		t.Errorf("Service alias not set: %q", resolved)
	}

	// Test resolve with context — inherited aliases should still work
	resolved := ctx.Resolve("${base}-${device}-${interface}")
	if resolved != "prefix-leaf1-ny-Ethernet0" {
		t.Errorf("Resolve() = %q", resolved)
	}
}

func TestAliasContextChaining(t *testing.T) {
	r := NewResolver(make(map[string]string), nil)

	result := NewAliasContext(r, "device1").
		WithInterface("eth0").
		WithService("svc1").
		Resolve("${device}-${interface}-${service}")

	if result != "device1-eth0-svc1" {
		t.Errorf("Chained context resolve = %q", result)
	}
}
