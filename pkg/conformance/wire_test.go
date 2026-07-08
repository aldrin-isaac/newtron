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

// TestHandlersUseConfigConverters enforces the Config() convention: an HTTP
// handler must not translate a wire request into a domain config inline —
// a composite literal of a newtron *Config type inside a handler file is a
// field-by-field copy that silently drops any field the request type grows
// (the same failure mode as the anonymous shadows, one layer deeper; six
// such copies existed when this sweep was written). The one sanctioned
// site per request type is its Config() method, next to the type in
// types.go — which is why the types files are not scanned here.
func TestHandlersUseConfigConverters(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "pkg/newtron/api")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var violations []string
	scanned := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "handler_") || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		scanned++
		path := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := cl.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "newtron" || !strings.HasSuffix(sel.Sel.Name, "Config") {
				return true
			}
			pos := fset.Position(cl.Pos())
			violations = append(violations, pos.String()+": inline newtron."+sel.Sel.Name+" literal — use the request type's Config() converter")
			return true
		})
	}
	if scanned < 3 {
		t.Fatalf("scanned only %d handler files — glob broke, sweep would be vacuous", scanned)
	}
	if len(violations) > 0 {
		t.Errorf("inline wire→domain translations found (the Config() convention owns that copy):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestIdenticalPairsUseDirectConversion closes the guard-strength gap the
// converter convention left: a hand-mapped converter body over a pair that
// is field-identical (names, types, order — tags ignored, exactly Go's
// struct-conversion rule) compiles today and silently drops any field the
// pair gains tomorrow. Where the language CAN check the copy, the body must
// let it: `return T(r)`. Hand-mapped bodies stay legitimate only where
// conversion is illegal — pairs whose fields include package-local named
// types (SetupDeviceOpts' nested RR) or whose field sets genuinely differ
// (every wire request that carries identity).
//
// Scope: methods named Config / internal / directPeer in the three converter
// homes. A pair counts as identical only when every field type is composed
// of builtins — a named struct type prints identically across packages while
// being a different type, so such pairs are conservatively skipped.
func TestIdenticalPairsUseDirectConversion(t *testing.T) {
	root := repoRoot(t)

	// Field shapes for every struct the converters touch.
	shapes := map[string][]string{}
	for _, src := range []string{"pkg/newtron/types.go", "pkg/newtron/api/types.go"} {
		collectShapes(t, filepath.Join(root, src), shapes)
	}
	nodeDir := filepath.Join(root, "pkg/newtron/network/node")
	entries, err := os.ReadDir(nodeDir)
	if err != nil {
		t.Fatalf("reading %s: %v", nodeDir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			collectShapes(t, filepath.Join(nodeDir, e.Name()), shapes)
		}
	}

	converterHomes := []string{
		"pkg/newtron/types.go", "pkg/newtron/api/types.go", "pkg/newtron/boundary.go",
	}
	var violations []string
	checked := 0
	for _, src := range converterHomes {
		path := filepath.Join(root, src)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil {
				continue
			}
			name := fd.Name.Name
			if name != "Config" && name != "internal" && name != "directPeer" {
				continue
			}
			recv, ret := converterEndpoints(fd)
			if recv == "" || ret == "" {
				continue
			}
			rShape, rOK := shapes[recv]
			tShape, tOK := shapes[ret]
			if !rOK || !tOK {
				continue
			}
			checked++
			if !shapesConvertible(rShape, tShape) {
				continue // genuinely divergent or non-builtin — hand-mapping is the only option
			}
			if !bodyIsDirectConversion(fd) {
				pos := fset.Position(fd.Pos())
				violations = append(violations,
					pos.String()+": "+recv+"."+name+"() hand-maps a field-identical pair — use `return "+ret+"(r)` so the compiler owns the copy")
			}
		}
	}
	if checked < 10 {
		t.Fatalf("checked only %d converters — parser broke, sweep would be vacuous", checked)
	}
	if len(violations) > 0 {
		t.Errorf("hand-mapped converters over identical pairs:\n  %s", strings.Join(violations, "\n  "))
	}
}

// collectShapes records exported struct field shapes as "name type" strings.
func collectShapes(t *testing.T, path string, out map[string][]string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		var shape []string
		for _, field := range st.Fields.List {
			typ := exprString(field.Type)
			for _, id := range field.Names {
				shape = append(shape, id.Name+" "+typ)
			}
		}
		out[ts.Name.Name] = shape
		return true
	})
}

// converterEndpoints returns the receiver type name and the returned type's
// bare name (package qualifier stripped) for a converter method.
func converterEndpoints(fd *ast.FuncDecl) (recv, ret string) {
	if len(fd.Recv.List) == 1 {
		if id, ok := fd.Recv.List[0].Type.(*ast.Ident); ok {
			recv = id.Name
		}
	}
	if fd.Type.Results != nil && len(fd.Type.Results.List) == 1 {
		switch rt := fd.Type.Results.List[0].Type.(type) {
		case *ast.Ident:
			ret = rt.Name
		case *ast.SelectorExpr:
			ret = rt.Sel.Name
		}
	}
	return recv, ret
}

// shapesConvertible reports whether two shapes are field-identical AND all
// field types are builtin-composed (a named struct type prints identically
// across packages while being a different type — conversion would be
// illegal, so those pairs are skipped, conservatively).
func shapesConvertible(a, b []string) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
		typ := strings.SplitN(a[i], " ", 2)[1]
		if !builtinComposed(typ) {
			return false
		}
	}
	return true
}

// builtinComposed reports whether a printed type is composed only of
// builtins and builtin containers.
func builtinComposed(typ string) bool {
	stripped := strings.NewReplacer(
		"[]", "", "map[", "", "]", "", "*", "", "string", "", "int64", "",
		"int", "", "bool", "", "float64", "", "byte", "", "any", "",
	).Replace(typ)
	return strings.TrimSpace(stripped) == ""
}

// bodyIsDirectConversion reports whether the method body is exactly
// `return T(r)`.
func bodyIsDirectConversion(fd *ast.FuncDecl) bool {
	if fd.Body == nil || len(fd.Body.List) != 1 {
		return false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return false
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	_, isIdent := call.Args[0].(*ast.Ident)
	return isIdent
}

// exprString renders a type expression compactly (enough for shape
// comparison — selectors keep only the bare name so cross-package twins
// compare equal, which builtinComposed then disqualifies).
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(v.X)
	case *ast.ArrayType:
		return "[]" + exprString(v.Elt)
	case *ast.MapType:
		return "map[" + exprString(v.Key) + "]" + exprString(v.Value)
	default:
		return "?"
	}
}
