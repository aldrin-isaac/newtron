package newtrun

import (
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// applyTemplate — context-aware encoding.
// ---------------------------------------------------------------------------

func TestApplyTemplate_URLContext_PathEscape(t *testing.T) {
	got, err := applyTemplate(
		"/nodes/{{target.device}}/x",
		map[string]string{"device": "switch1"},
		nil,
		nil,
		ctxURL,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "/nodes/switch1/x" {
		t.Errorf("got %q, want /node/switch1/x", got)
	}
}

// applyTemplateURL splits at the first '?' and applies PathEscape to
// the path portion, QueryEscape to the query string. PathEscape leaves
// '&', '=', '+' unescaped, so a string param dropped into a query
// position via the path-context path would inject extra parameters.
func TestApplyTemplateURL_QueryPositionUsesQueryEscape(t *testing.T) {
	got, err := applyTemplateURL(
		"/search?q={{param.q}}",
		nil,
		map[string]any{"q": "a&evil=1"},
		nil,
		)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := "/search?q=a%26evil%3D1"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyTemplateURL_PathPositionStillUsesPathEscape(t *testing.T) {
	got, err := applyTemplateURL(
		"/nodes/{{target.device}}",
		map[string]string{"device": "switch1"},
		nil,
		nil,
		)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "/nodes/switch1" {
		t.Errorf("got %q, want /node/switch1", got)
	}
}

func TestApplyTemplateURL_BothPathAndQuery(t *testing.T) {
	got, err := applyTemplateURL(
		"/nodes/{{target.device}}/route?filter={{param.f}}",
		map[string]string{"device": "switch1"},
		map[string]any{"f": "vrf=red&owner=me"},
		nil,
		)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// Path: "switch1" passes PathEscape unchanged. Query: '&' and '='
	// in the filter value get QueryEscape'd so they don't smuggle
	// extra query parameters.
	want := "/nodes/switch1/route?filter=vrf%3Dred%26owner%3Dme"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyTemplate_URLContext_EscapesParamWithSlash(t *testing.T) {
	// A parameter value that contains URL-unsafe characters must be
	// path-escaped, defending against path traversal injection.
	got, err := applyTemplate(
		"/nodes/{{param.intf}}/x",
		nil,
		map[string]any{"intf": "Ethernet0/1"},
		nil,
		ctxURL,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "/nodes/Ethernet0%2F1/x" {
		t.Errorf("got %q, want path-escaped form", got)
	}
}

func TestApplyTemplate_ShellContext_SingleQuoteWraps(t *testing.T) {
	got, err := applyTemplate(
		"show interface {{param.name}}",
		nil,
		map[string]any{"name": "Ethernet0"},
		nil,
		ctxShell,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "show interface 'Ethernet0'" {
		t.Errorf("got %q, want single-quoted form", got)
	}
}

func TestApplyTemplate_ShellContext_EscapesInternalQuote(t *testing.T) {
	// The POSIX idiom for embedding a single quote inside a single-
	// quoted string is `'"'"'` (close, double-quoted-single, reopen).
	got, err := applyTemplate(
		"echo {{param.x}}",
		nil,
		map[string]any{"x": `a'b`},
		nil,
		ctxShell,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := `echo 'a'"'"'b'`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// newtron-cli execs argv via strings.Fields with no shell in the
// middle; substituted values must NOT be shell-quoted or literal
// single quotes appear inside argv elements. ExpandStep picks the
// substitution context by action — ctxShell for host-exec, ctxRaw
// for newtron-cli.
func TestExpandStep_NewtronCLICommandUsesRawContext(t *testing.T) {
	step := Step{
		Action:  ActionNewtronCLI,
		Command: "service apply Ethernet0 {{param.opt}}",
	}
	expanded, err := ExpandStep(step, nil, map[string]any{"opt": "transit"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.Command != "service apply Ethernet0 transit" {
		t.Errorf("Command = %q, want raw (no shell quotes)", expanded.Command)
	}
}

func TestExpandStep_HostExecCommandUsesShellContext(t *testing.T) {
	step := Step{
		Action:  ActionHostExec,
		Command: "echo {{param.msg}}",
	}
	expanded, err := ExpandStep(step, nil, map[string]any{"msg": "hello world"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.Command != "echo 'hello world'" {
		t.Errorf("Command = %q, want shell-quoted", expanded.Command)
	}
}

func TestApplyTemplate_ShellContext_DefendsAgainstCommandInjection(t *testing.T) {
	// The attempted injection `; rm -rf /` must be wrapped as a single
	// argv, not interpreted as a command separator.
	got, err := applyTemplate(
		"echo {{param.x}}",
		nil,
		map[string]any{"x": "; rm -rf /"},
		nil,
		ctxShell,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(got, "'; rm -rf /'") {
		t.Errorf("injection should be wrapped in quotes; got %q", got)
	}
}

func TestApplyTemplate_JQContext_QuotesStrings(t *testing.T) {
	// String params in JQ context emit as JSON-quoted form. The user
	// writes `{{param.X}}` WITHOUT surrounding quotes; the engine adds
	// them.
	got, err := applyTemplate(
		".admin_status == {{param.status}}",
		nil,
		map[string]any{"status": "up"},
		nil,
		ctxJQ,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != `.admin_status == "up"` {
		t.Errorf("got %q, want JSON-quoted form", got)
	}
}

func TestApplyTemplate_JQContext_NumericLiteral(t *testing.T) {
	got, err := applyTemplate(
		".mtu == {{param.mtu}}",
		nil,
		map[string]any{"mtu": 9100},
		nil,
		ctxJQ,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != ".mtu == 9100" {
		t.Errorf("got %q, want numeric literal form", got)
	}
}

func TestApplyTemplate_JQContext_BoolLiteral(t *testing.T) {
	got, err := applyTemplate(
		".enabled == {{param.active}}",
		nil,
		map[string]any{"active": true},
		nil,
		ctxJQ,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != ".enabled == true" {
		t.Errorf("got %q, want bool literal", got)
	}
}

func TestApplyTemplate_JQContext_DefendsAgainstStringInjection(t *testing.T) {
	// An attempted JQ injection embeds a closing quote followed by
	// extra expression. JSON-quoting escapes the embedded quote and
	// closes the string literal cleanly — JQ then sees a syntactically
	// closed string, not a continuation.
	got, err := applyTemplate(
		".admin_status == {{param.status}}",
		nil,
		map[string]any{"status": `up" or true == "x`},
		nil,
		ctxJQ,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(got, `\"`) {
		t.Errorf("embedded quote not escaped: %q", got)
	}
}

func TestApplyTemplate_RawContext_NoEscaping(t *testing.T) {
	got, err := applyTemplate(
		"contains: {{param.x}}",
		nil,
		map[string]any{"x": "anything goes"},
		nil,
		ctxRaw,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "contains: anything goes" {
		t.Errorf("got %q, want unmodified", got)
	}
}

func TestApplyTemplate_UndefinedTargetRefError(t *testing.T) {
	_, err := applyTemplate(
		"/nodes/{{target.missing}}/x",
		map[string]string{},
		nil,
		nil,
		ctxURL,
	)
	if err == nil || !strings.Contains(err.Error(), "undefined target") {
		t.Errorf("err = %v, want undefined-target error", err)
	}
}

func TestApplyTemplate_UndefinedParamRefError(t *testing.T) {
	_, err := applyTemplate(
		"echo {{param.missing}}",
		nil,
		map[string]any{},
		nil,
		ctxShell,
	)
	if err == nil || !strings.Contains(err.Error(), "undefined param") {
		t.Errorf("err = %v, want undefined-param error", err)
	}
}

func TestApplyTemplate_EmptyStringPassthrough(t *testing.T) {
	got, err := applyTemplate("", nil, nil, nil, ctxURL)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestApplyTemplate_MultipleSubstitutionsSameString(t *testing.T) {
	got, err := applyTemplate(
		"/nodes/{{target.device}}/iface/{{target.interface}}",
		map[string]string{"device": "s1", "interface": "Eth0"},
		nil,
		nil,
		ctxURL,
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "/nodes/s1/iface/Eth0" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// expandMapAny — typed full-token vs inline interpolation.
// ---------------------------------------------------------------------------

func TestExpandMapAny_FullTokenPreservesIntType(t *testing.T) {
	in := map[string]any{"mtu": "{{param.mtu}}"}
	got, err := expandMapAny(in, nil, map[string]any{"mtu": 9100}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v, ok := got["mtu"].(int); !ok || v != 9100 {
		t.Errorf("mtu = %v (%T), want int 9100", got["mtu"], got["mtu"])
	}
}

func TestExpandMapAny_FullTokenPreservesBoolType(t *testing.T) {
	in := map[string]any{"enabled": "{{param.flag}}"}
	got, err := expandMapAny(in, nil, map[string]any{"flag": true}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if v, ok := got["enabled"].(bool); !ok || v != true {
		t.Errorf("enabled = %v (%T), want bool true", got["enabled"], got["enabled"])
	}
}

func TestExpandMapAny_InlinedStringStaysString(t *testing.T) {
	in := map[string]any{"msg": "value is {{param.x}}"}
	got, err := expandMapAny(in, nil, map[string]any{"x": "up"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got["msg"] != "value is up" {
		t.Errorf("msg = %v, want concat string", got["msg"])
	}
}

func TestExpandMapAny_NestedMapsRecurse(t *testing.T) {
	in := map[string]any{
		"outer": map[string]any{
			"inner": "{{param.x}}",
		},
	}
	got, err := expandMapAny(in, nil, map[string]any{"x": "value"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	inner := got["outer"].(map[string]any)["inner"]
	if inner != "value" {
		t.Errorf("nested map: got %v", inner)
	}
}

func TestExpandMapAny_SlicesRecurse(t *testing.T) {
	in := map[string]any{
		"list": []any{"{{param.x}}", "static"},
	}
	got, err := expandMapAny(in, nil, map[string]any{"x": "value"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	list := got["list"].([]any)
	if list[0] != "value" || list[1] != "static" {
		t.Errorf("slice: got %v", list)
	}
}

func TestExpandMapAny_NonStringScalarsPassThrough(t *testing.T) {
	in := map[string]any{"count": 42, "active": true}
	got, err := expandMapAny(in, nil, nil, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got["count"] != 42 || got["active"] != true {
		t.Errorf("scalars: got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ExpandStep
// ---------------------------------------------------------------------------

func TestExpandStep_URLAndParamsAndJQ(t *testing.T) {
	step := Step{
		URL: "/nodes/{{target.device}}/interfaces/{{target.interface}}",
		Params: map[string]any{
			"value": "{{param.admin_status}}",
		},
		Expect: &ExpectBlock{
			JQ: ".admin_status == {{param.admin_status}}",
		},
	}
	expanded, err := ExpandStep(step,
		map[string]string{"device": "s1", "interface": "Eth0"},
		map[string]any{"admin_status": "up"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.URL != "/nodes/s1/interfaces/Eth0" {
		t.Errorf("URL = %q", expanded.URL)
	}
	if expanded.Params["value"] != "up" {
		t.Errorf("Params.value = %v", expanded.Params["value"])
	}
	if expanded.Expect.JQ != `.admin_status == "up"` {
		t.Errorf("Expect.JQ = %q", expanded.Expect.JQ)
	}
}

func TestExpandStep_DoesNotMutateOriginal(t *testing.T) {
	// Repeat correctness: every iteration must expand from the same
	// pristine source step.
	step := Step{
		URL:    "/nodes/{{target.device}}/x",
		Params: map[string]any{"v": "{{param.x}}"},
	}
	origURL := step.URL
	_, err := ExpandStep(step,
		map[string]string{"device": "s1"},
		map[string]any{"x": "value"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if step.URL != origURL {
		t.Errorf("source URL mutated: %q", step.URL)
	}
	if step.Params["v"] != "{{param.x}}" {
		t.Errorf("source Params mutated: %v", step.Params)
	}
}

func TestExpandStep_ExpandsBatch(t *testing.T) {
	step := Step{
		Batch: []BatchCall{
			{Method: "GET", URL: "/nodes/{{target.device}}/a"},
			{Method: "POST", URL: "/nodes/{{target.device}}/b", Params: map[string]any{"v": "{{param.x}}"}},
		},
	}
	expanded, err := ExpandStep(step,
		map[string]string{"device": "s1"},
		map[string]any{"x": "value"}, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.Batch[0].URL != "/nodes/s1/a" {
		t.Errorf("batch[0].URL = %q", expanded.Batch[0].URL)
	}
	if expanded.Batch[1].URL != "/nodes/s1/b" {
		t.Errorf("batch[1].URL = %q", expanded.Batch[1].URL)
	}
	if expanded.Batch[1].Params["v"] != "value" {
		t.Errorf("batch[1].Params.v = %v", expanded.Batch[1].Params["v"])
	}
}

func TestExpandStep_ExpandsExpectContains(t *testing.T) {
	step := Step{
		Expect: &ExpectBlock{Contains: "interface {{target.interface}} is up"},
	}
	expanded, err := ExpandStep(step,
		map[string]string{"interface": "Eth0"},
		nil, nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if expanded.Expect.Contains != "interface Eth0 is up" {
		t.Errorf("Contains = %q", expanded.Expect.Contains)
	}
}

func TestExpandStep_PropagatesError(t *testing.T) {
	step := Step{URL: "/nodes/{{target.missing}}/x"}
	_, err := ExpandStep(step, map[string]string{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "url") {
		t.Errorf("err = %v, want url-prefixed", err)
	}
}

// ---------------------------------------------------------------------------
// CollectTemplateReferences
// ---------------------------------------------------------------------------

func TestCollectTemplateReferences_GathersAllSurfaces(t *testing.T) {
	step := Step{
		URL:     "/nodes/{{target.device}}",
		Command: "echo {{param.a}}",
		Params: map[string]any{
			"nested": map[string]any{"k": "{{param.b}}"},
		},
		Batch: []BatchCall{
			{URL: "/x/{{target.interface}}", Params: map[string]any{"v": "{{param.c}}"}},
		},
		Expect: &ExpectBlock{
			JQ:       ".x == {{param.d}}",
			Contains: "saw {{param.e}}",
		},
	}
	targets, params, hasDevice := CollectTemplateReferences(step)
	wantTargets := map[string]bool{"device": true, "interface": true}
	for _, tt := range targets {
		if !wantTargets[tt] {
			t.Errorf("unexpected target ref %q", tt)
		}
		delete(wantTargets, tt)
	}
	if len(wantTargets) > 0 {
		t.Errorf("missing target refs: %v", wantTargets)
	}
	wantParams := map[string]bool{"a": true, "b": true, "c": true, "d": true, "e": true}
	for _, p := range params {
		if !wantParams[p] {
			t.Errorf("unexpected param ref %q", p)
		}
		delete(wantParams, p)
	}
	if len(wantParams) > 0 {
		t.Errorf("missing param refs: %v", wantParams)
	}
	if hasDevice {
		t.Errorf("hasDevice = true, want false (no {{device}} token)")
	}
}

func TestCollectTemplateReferences_DetectsDeviceToken(t *testing.T) {
	step := Step{URL: "/nodes/{{device}}/x"}
	_, _, hasDevice := CollectTemplateReferences(step)
	if !hasDevice {
		t.Errorf("hasDevice = false, want true")
	}
}

func TestCollectTemplateReferences_Deduplicates(t *testing.T) {
	step := Step{
		URL:     "/{{target.device}}/{{target.device}}",
		Command: "{{target.device}}",
	}
	targets, _, _ := CollectTemplateReferences(step)
	if !reflect.DeepEqual(targets, []string{"device"}) {
		t.Errorf("targets = %v, want [device] (deduplicated)", targets)
	}
}

func TestCollectTemplateReferences_EmptyStep(t *testing.T) {
	step := Step{}
	targets, params, hasDevice := CollectTemplateReferences(step)
	if len(targets) != 0 || len(params) != 0 || hasDevice {
		t.Errorf("empty step: targets=%v params=%v hasDevice=%v", targets, params, hasDevice)
	}
}

// ---------------------------------------------------------------------------
// jsonQuote / stringifyScalar helpers — directly cover the encoding choices.
// ---------------------------------------------------------------------------

func TestStringifyScalar_Forms(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hi", "hi"},
		{42, "42"},
		{int64(42), "42"},
		{float64(3.14), "3.14"},
		{true, "true"},
		{false, "false"},
	}
	for _, c := range cases {
		if got := stringifyScalar(c.in); got != c.want {
			t.Errorf("stringifyScalar(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJSONQuote_EscapesQuotesAndBackslashes(t *testing.T) {
	got := jsonQuote(`he said "hi"\back`)
	want := `"he said \"hi\"\\back"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
