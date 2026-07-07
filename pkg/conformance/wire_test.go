package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoWireShadowStructs is the governance sweep for the wire-decode
// boundary (RCA-049). An anonymous struct in a handler or the HTTP client
// that mirrors a named request/config type is a silent-drop trap: the
// moment the named type grows a field, the anonymous copy keeps decoding
// (or sending) without it, and the value dies on the wire with no error
// anywhere. The evpn flag died exactly this way — twice, at two layers —
// and the ACL rule body existed as SIX hand-kept copies of one field set.
//
// The rule: within pkg/newtron/api and pkg/newtron/client, an anonymous
// struct whose json tags (3 or more) are all tags of one named struct in
// pkg/newtron/types.go must not exist — decode or send the named type.
// One- and two-tag bodies stay allowed: they are identity params (remove
// verbs take key fields only — {acl, rule}, {policy, seq}) and cannot be
// told apart from shadows by tags alone. The floor is honest, not safe:
// a true two-field shadow of a three-field type would pass. Every shadow
// found in the wild carried 3+ fields.
//
// Out of scope, documented honestly: named→named field copies (a handler
// translating a request type into a domain config inline) drop new fields
// the same way but are single-sited and adjacent to their decode; the
// Config() converter convention (types.go) is the fix where the copy
// count or distance grows.
func TestNoWireShadowStructs(t *testing.T) {
	root := repoRoot(t)

	named := namedTagSets(t, filepath.Join(root, "pkg/newtron/types.go"))
	if len(named) < 5 {
		t.Fatalf("parsed only %d named wire types from types.go — parser broke, sweep would be vacuous", len(named))
	}

	scanDirs := []string{
		filepath.Join(root, "pkg/newtron/api"),
		filepath.Join(root, "pkg/newtron/client"),
	}
	var violations []string
	for _, dir := range scanDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			violations = append(violations, shadowsInFile(t, path, named)...)
		}
	}

	if len(violations) > 0 {
		t.Errorf("anonymous wire-shadow structs found (decode/send the named type instead — RCA-049):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// namedTagSets parses types.go and returns, per exported struct type with
// at least two json-tagged fields, the set of its json tag names.
func namedTagSets(t *testing.T, path string) map[string]map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	out := map[string]map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || !ts.Name.IsExported() {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		tags := jsonTags(st)
		if len(tags) >= 2 {
			out[ts.Name.Name] = tags
		}
		return true
	})
	return out
}

// shadowsInFile returns a violation line for every anonymous struct in the
// file whose json tags (>= 2) are all tags of one named type.
func shadowsInFile(t *testing.T, path string, named map[string]map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	// Collect named struct declarations in THIS file so their bodies are
	// not scanned as anonymous (a declared type is discoverable and owned;
	// the sweep targets literals with no name).
	declared := map[*ast.StructType]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			if st, ok := ts.Type.(*ast.StructType); ok {
				declared[st] = true
			}
		}
		return true
	})

	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || declared[st] {
			return true
		}
		tags := jsonTags(st)
		if len(tags) < 3 {
			return true
		}
		for typeName, namedTags := range named {
			subset := true
			for tag := range tags {
				if !namedTags[tag] {
					subset = false
					break
				}
			}
			if subset {
				pos := fset.Position(st.Pos())
				var tagList []string
				for tag := range tags {
					tagList = append(tagList, tag)
				}
				sort.Strings(tagList)
				out = append(out, pos.String()+": anonymous struct {"+strings.Join(tagList, ", ")+"} shadows newtron."+typeName)
				break
			}
		}
		return true
	})
	return out
}

// jsonTags returns the json tag names of a struct's fields (ignoring "-"
// and untagged fields).
func jsonTags(st *ast.StructType) map[string]bool {
	tags := map[string]bool{}
	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		raw := strings.Trim(field.Tag.Value, "`")
		for _, part := range strings.Split(raw, " ") {
			if !strings.HasPrefix(part, `json:"`) {
				continue
			}
			name := strings.TrimPrefix(part, `json:"`)
			name = strings.SplitN(strings.TrimSuffix(name, `"`), ",", 2)[0]
			if name != "" && name != "-" {
				tags[name] = true
			}
		}
	}
	return tags
}
