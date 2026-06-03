package newtrun

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// ParameterSpec — YAML shorthand vs verbose unmarshaling.
// ---------------------------------------------------------------------------

func TestParameterSpec_UnmarshalShorthandString(t *testing.T) {
	var p ParameterSpec
	if err := yaml.Unmarshal([]byte(`up`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != ParameterTypeString {
		t.Errorf("Type = %q, want %q", p.Type, ParameterTypeString)
	}
	if p.Default != "up" {
		t.Errorf("Default = %v, want %q", p.Default, "up")
	}
}

func TestParameterSpec_UnmarshalShorthandInt(t *testing.T) {
	var p ParameterSpec
	if err := yaml.Unmarshal([]byte(`9100`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != ParameterTypeInt {
		t.Errorf("Type = %q, want %q", p.Type, ParameterTypeInt)
	}
	if p.Default != 9100 {
		t.Errorf("Default = %v, want %d", p.Default, 9100)
	}
}

func TestParameterSpec_UnmarshalShorthandBool(t *testing.T) {
	var p ParameterSpec
	if err := yaml.Unmarshal([]byte(`true`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != ParameterTypeBool {
		t.Errorf("Type = %q, want %q", p.Type, ParameterTypeBool)
	}
	if p.Default != true {
		t.Errorf("Default = %v, want true", p.Default)
	}
}

func TestParameterSpec_UnmarshalVerboseEnum(t *testing.T) {
	var p ParameterSpec
	body := `
type: enum
values: [up, down]
default: up
`
	if err := yaml.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != ParameterTypeEnum {
		t.Errorf("Type = %q, want %q", p.Type, ParameterTypeEnum)
	}
	if !reflect.DeepEqual(p.Values, []string{"up", "down"}) {
		t.Errorf("Values = %v, want [up down]", p.Values)
	}
	if p.Default != "up" {
		t.Errorf("Default = %v, want up", p.Default)
	}
}

func TestParameterSpec_UnmarshalVerboseIntWithBounds(t *testing.T) {
	var p ParameterSpec
	body := `
type: int
min: 1500
max: 9216
default: 9100
`
	if err := yaml.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != ParameterTypeInt {
		t.Errorf("Type = %q, want int", p.Type)
	}
	if p.Min == nil || *p.Min != 1500 {
		t.Errorf("Min = %v, want 1500", p.Min)
	}
	if p.Max == nil || *p.Max != 9216 {
		t.Errorf("Max = %v, want 9216", p.Max)
	}
	if p.Default != 9100 {
		t.Errorf("Default = %v, want 9100", p.Default)
	}
}

func TestParameterSpec_UnmarshalVerboseDefaultsToString(t *testing.T) {
	// Verbose form with no type: field defaults to string.
	var p ParameterSpec
	if err := yaml.Unmarshal([]byte("default: foo"), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Type != ParameterTypeString {
		t.Errorf("Type = %q, want string", p.Type)
	}
}

// ---------------------------------------------------------------------------
// ParameterSpec.ValidateDeclaration
// ---------------------------------------------------------------------------

func TestParameterSpec_ValidateDeclaration_UnknownType(t *testing.T) {
	p := ParameterSpec{Type: "carrots"}
	if err := p.ValidateDeclaration(); err == nil || !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("err = %v, want unknown-type error", err)
	}
}

func TestParameterSpec_ValidateDeclaration_EnumNeedsValues(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeEnum}
	if err := p.ValidateDeclaration(); err == nil || !strings.Contains(err.Error(), "values is required") {
		t.Errorf("err = %v, want values-required error", err)
	}
}

func TestParameterSpec_ValidateDeclaration_DefaultViolatesEnum(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeEnum, Values: []string{"a", "b"}, Default: "z"}
	if err := p.ValidateDeclaration(); err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("err = %v, want default-violation error", err)
	}
}

func TestParameterSpec_ValidateDeclaration_DefaultViolatesIntMin(t *testing.T) {
	one := 100
	p := ParameterSpec{Type: ParameterTypeInt, Min: &one, Default: 50}
	if err := p.ValidateDeclaration(); err == nil || !strings.Contains(err.Error(), "below min") {
		t.Errorf("err = %v, want below-min error", err)
	}
}

// ---------------------------------------------------------------------------
// ParameterSpec.Coerce
// ---------------------------------------------------------------------------

func TestParameterSpec_Coerce_String(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeString}
	if v, err := p.Coerce("hi"); err != nil || v != "hi" {
		t.Errorf("Coerce(string): v=%v err=%v", v, err)
	}
	if _, err := p.Coerce(42); err == nil {
		t.Errorf("Coerce(int as string): expected error")
	}
}

func TestParameterSpec_Coerce_IntForms(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeInt}
	for _, in := range []any{42, int64(42), float64(42)} {
		v, err := p.Coerce(in)
		if err != nil || v != 42 {
			t.Errorf("Coerce(%T %v): v=%v err=%v", in, in, v, err)
		}
	}
	if _, err := p.Coerce(3.5); err == nil {
		t.Errorf("Coerce(fractional float): expected error")
	}
}

func TestParameterSpec_Coerce_IntBounds(t *testing.T) {
	min, max := 10, 20
	p := ParameterSpec{Type: ParameterTypeInt, Min: &min, Max: &max}
	if _, err := p.Coerce(5); err == nil {
		t.Errorf("Coerce(below min): expected error")
	}
	if _, err := p.Coerce(25); err == nil {
		t.Errorf("Coerce(above max): expected error")
	}
	if v, err := p.Coerce(15); err != nil || v != 15 {
		t.Errorf("Coerce(in range): v=%v err=%v", v, err)
	}
}

func TestParameterSpec_Coerce_Bool(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeBool}
	if v, err := p.Coerce(true); err != nil || v != true {
		t.Errorf("Coerce(bool): v=%v err=%v", v, err)
	}
	if _, err := p.Coerce("true"); err == nil {
		t.Errorf("Coerce(string as bool): expected error")
	}
}

func TestParameterSpec_Coerce_Enum(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeEnum, Values: []string{"a", "b"}}
	if v, err := p.Coerce("a"); err != nil || v != "a" {
		t.Errorf("Coerce(in enum): v=%v err=%v", v, err)
	}
	if _, err := p.Coerce("c"); err == nil || !strings.Contains(err.Error(), "not in") {
		t.Errorf("Coerce(out of enum): expected not-in error, got %v", err)
	}
}

func TestParameterSpec_Coerce_IPv4(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeIPv4}
	if v, err := p.Coerce("10.0.0.1"); err != nil || v != "10.0.0.1" {
		t.Errorf("Coerce(valid IPv4): v=%v err=%v", v, err)
	}
	if _, err := p.Coerce("not-an-ip"); err == nil {
		t.Errorf("Coerce(invalid IPv4): expected error")
	}
	if _, err := p.Coerce("::1"); err == nil {
		t.Errorf("Coerce(IPv6 as IPv4): expected error")
	}
}

func TestParameterSpec_Coerce_CIDR(t *testing.T) {
	p := ParameterSpec{Type: ParameterTypeCIDR}
	if v, err := p.Coerce("10.0.0.0/24"); err != nil || v != "10.0.0.0/24" {
		t.Errorf("Coerce(valid CIDR): v=%v err=%v", v, err)
	}
	if _, err := p.Coerce("10.0.0.0"); err == nil {
		t.Errorf("Coerce(missing prefix): expected error")
	}
}

// ---------------------------------------------------------------------------
// Suite.IsParameterized / TargetIterations
// ---------------------------------------------------------------------------

func TestSuite_IsParameterized(t *testing.T) {
	cases := []struct {
		name string
		s    Suite
		want bool
	}{
		{"empty", Suite{}, false},
		{"only-targets", Suite{Targets: map[string][]string{"devices": {"s1"}}}, true},
		{"only-params", Suite{Parameters: map[string]ParameterSpec{"x": {Type: ParameterTypeString}}}, true},
		{"both", Suite{
			Targets:    map[string][]string{"devices": {"s1"}},
			Parameters: map[string]ParameterSpec{"x": {Type: ParameterTypeString}},
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.s.IsParameterized(); got != c.want {
				t.Errorf("IsParameterized = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSuite_TargetIterations_Empty(t *testing.T) {
	s := Suite{}
	it := s.TargetIterations()
	if len(it) != 1 || it[0] != nil {
		t.Errorf("empty suite iterations = %v, want [nil]", it)
	}
}

func TestSuite_TargetIterations_CrossProduct(t *testing.T) {
	s := Suite{Targets: map[string][]string{
		"devices":    {"s1", "s2"},
		"interfaces": {"Eth0", "Eth4"},
	}}
	got := s.TargetIterations()
	want := []map[string]string{
		{"device": "s1", "interface": "Eth0"},
		{"device": "s1", "interface": "Eth4"},
		{"device": "s2", "interface": "Eth0"},
		{"device": "s2", "interface": "Eth4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TargetIterations = %v, want %v", got, want)
	}
}

func TestSuite_TargetIterations_SingleDim(t *testing.T) {
	s := Suite{Targets: map[string][]string{"devices": {"s1", "s2", "s3"}}}
	got := s.TargetIterations()
	want := []map[string]string{
		{"device": "s1"},
		{"device": "s2"},
		{"device": "s3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TargetIterations = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Suite.EffectiveTargets — whitelist, overrides, unknown keys.
// ---------------------------------------------------------------------------

func TestSuite_EffectiveTargets_DefaultsCopied(t *testing.T) {
	s := Suite{Targets: map[string][]string{"devices": {"s1", "s2"}}}
	got, err := s.EffectiveTargets(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(got, s.Targets) {
		t.Errorf("got %v, want %v", got, s.Targets)
	}
	// Mutating the result must not mutate the suite's slice.
	got["devices"][0] = "MUTATED"
	if s.Targets["devices"][0] == "MUTATED" {
		t.Errorf("EffectiveTargets returned a shared backing array")
	}
}

func TestSuite_EffectiveTargets_OverrideReplaces(t *testing.T) {
	s := Suite{Targets: map[string][]string{
		"devices":    {"s1", "s2"},
		"interfaces": {"Eth0", "Eth4"},
	}}
	got, err := s.EffectiveTargets(map[string][]string{"devices": {"s3"}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(got["devices"], []string{"s3"}) {
		t.Errorf("devices = %v, want [s3]", got["devices"])
	}
	// Unchanged key inherits.
	if !reflect.DeepEqual(got["interfaces"], []string{"Eth0", "Eth4"}) {
		t.Errorf("interfaces inheritance broke: %v", got["interfaces"])
	}
}

func TestSuite_EffectiveTargets_RejectsUnknownKey(t *testing.T) {
	s := Suite{Targets: map[string][]string{"devices": {"s1"}}}
	_, err := s.EffectiveTargets(map[string][]string{"unicorns": {"u1"}})
	if err == nil || !strings.Contains(err.Error(), "unknown target dimension") {
		t.Errorf("err = %v, want unknown-dimension error", err)
	}
}

func TestSuite_EffectiveTargets_RejectsEmptyOverride(t *testing.T) {
	s := Suite{Targets: map[string][]string{"devices": {"s1"}}}
	_, err := s.EffectiveTargets(map[string][]string{"devices": nil})
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Errorf("err = %v, want empty-override error", err)
	}
}

func TestSuite_EffectiveTargets_RejectsInjection(t *testing.T) {
	s := Suite{Targets: map[string][]string{"devices": {"s1"}}}
	bad := []string{"; rm -rf /", "s1/../escape", "s1 with space", "s1\"quoted", "s1$VAR"}
	for _, b := range bad {
		_, err := s.EffectiveTargets(map[string][]string{"devices": {b}})
		if err == nil {
			t.Errorf("value %q: expected rejection", b)
		}
	}
}

// ---------------------------------------------------------------------------
// Suite.EffectiveParameters
// ---------------------------------------------------------------------------

func TestSuite_EffectiveParameters_DefaultsUsedWhenNoOverride(t *testing.T) {
	s := Suite{Parameters: map[string]ParameterSpec{
		"admin_status": {Type: ParameterTypeString, Default: "up"},
	}}
	got, err := s.EffectiveParameters(nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got["admin_status"] != "up" {
		t.Errorf("default not applied: got %v", got["admin_status"])
	}
}

func TestSuite_EffectiveParameters_OverrideWins(t *testing.T) {
	s := Suite{Parameters: map[string]ParameterSpec{
		"admin_status": {Type: ParameterTypeEnum, Values: []string{"up", "down"}, Default: "up"},
	}}
	got, err := s.EffectiveParameters(map[string]any{"admin_status": "down"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got["admin_status"] != "down" {
		t.Errorf("override not applied: got %v", got["admin_status"])
	}
}

func TestSuite_EffectiveParameters_RejectsUnknownOverride(t *testing.T) {
	s := Suite{Parameters: map[string]ParameterSpec{
		"admin_status": {Type: ParameterTypeString, Default: "up"},
	}}
	_, err := s.EffectiveParameters(map[string]any{"made_up": "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown parameter") {
		t.Errorf("err = %v, want unknown-parameter error", err)
	}
}

func TestSuite_EffectiveParameters_TypeMismatchRejected(t *testing.T) {
	s := Suite{Parameters: map[string]ParameterSpec{
		"mtu": {Type: ParameterTypeInt, Default: 9100},
	}}
	_, err := s.EffectiveParameters(map[string]any{"mtu": "not-an-int"})
	if err == nil {
		t.Errorf("expected type-mismatch rejection")
	}
}

func TestSuite_EffectiveParameters_RequiredWithoutDefault(t *testing.T) {
	s := Suite{Parameters: map[string]ParameterSpec{
		"peer_ip": {Type: ParameterTypeIPv4, Required: true},
	}}
	_, err := s.EffectiveParameters(nil)
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("err = %v, want required-no-default error", err)
	}
	// Supplied override satisfies the requirement.
	got, err := s.EffectiveParameters(map[string]any{"peer_ip": "10.0.0.1"})
	if err != nil {
		t.Fatalf("with override: err = %v", err)
	}
	if got["peer_ip"] != "10.0.0.1" {
		t.Errorf("with override: got %v", got["peer_ip"])
	}
}

// ---------------------------------------------------------------------------
// ScenarioIsParameterized
// ---------------------------------------------------------------------------

func TestScenarioIsParameterized_DetectsURL(t *testing.T) {
	sc := &Scenario{Steps: []Step{{URL: "/node/{{target.device}}/x"}}}
	if !ScenarioIsParameterized(sc) {
		t.Errorf("expected parameterized")
	}
}

func TestScenarioIsParameterized_DetectsParamInJQ(t *testing.T) {
	sc := &Scenario{Steps: []Step{{
		Expect: &ExpectBlock{JQ: ".admin_status == {{param.admin_status}}"},
	}}}
	if !ScenarioIsParameterized(sc) {
		t.Errorf("expected parameterized")
	}
}

func TestScenarioIsParameterized_DetectsParamInParams(t *testing.T) {
	sc := &Scenario{Steps: []Step{{
		Params: map[string]any{"value": "{{param.x}}"},
	}}}
	if !ScenarioIsParameterized(sc) {
		t.Errorf("expected parameterized")
	}
}

func TestScenarioIsParameterized_EmbeddedTargetIsFalse(t *testing.T) {
	sc := &Scenario{Steps: []Step{{URL: "/node/{{device}}/x"}}}
	if ScenarioIsParameterized(sc) {
		t.Errorf("scenario with {{device}} is embedded-target, not parameterized")
	}
}

func TestScenarioIsParameterized_NoTemplatesFalse(t *testing.T) {
	sc := &Scenario{Steps: []Step{{URL: "/static/path"}}}
	if ScenarioIsParameterized(sc) {
		t.Errorf("static URL: expected false")
	}
}

// ---------------------------------------------------------------------------
// LoadSuite happy path + error surfaces.
// ---------------------------------------------------------------------------

// writeSuiteDir creates a temporary suite directory and writes the
// supplied files. Returns the directory path. Files map: name → body.
func writeSuiteDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestLoadSuite_HappyPath(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
targets:
  devices: [s1, s2]
parameters:
  admin_status:
    type: enum
    values: [up, down]
    default: up
`,
		"00-noop.yaml": `name: noop
steps:
  - name: wait
    action: wait
    duration: 1s
`,
		"01-rollout.yaml": `name: rollout
steps:
  - name: set
    action: newtron
    method: POST
    url: /node/{{target.device}}/x
    params:
      value: "{{param.admin_status}}"
`,
	})
	suite, err := LoadSuite(dir)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	if suite.Name != "demo" || suite.Topology != "synthetic" {
		t.Errorf("metadata mismatch: %+v", suite)
	}
	if len(suite.Scenarios) != 2 {
		t.Fatalf("scenarios: got %d, want 2", len(suite.Scenarios))
	}
	if !suite.IsParameterized() {
		t.Errorf("expected parameterized suite")
	}
}

func TestLoadSuite_MissingSuiteYAML(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"00-noop.yaml": `name: noop
steps:
  - name: wait
    action: wait
    duration: 1s
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "suite.yaml") {
		t.Errorf("err = %v, want missing-suite.yaml error", err)
	}
}

func TestLoadSuite_MissingTopology(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "topology is required") {
		t.Errorf("err = %v, want topology-required error", err)
	}
}

func TestLoadSuite_MissingName(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `topology: synthetic
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want name-required error", err)
	}
}

func TestLoadSuite_RejectsScenarioWithTopology(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
`,
		"00-bad.yaml": `name: bad
topology: synthetic
steps:
  - name: wait
    action: wait
    duration: 1s
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "topology is set on suite.yaml") {
		t.Errorf("err = %v, want scenario-topology error", err)
	}
}

func TestLoadSuite_RejectsScenarioWithPlatform(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
`,
		"00-bad.yaml": `name: bad
platform: sonic-vs
steps:
  - name: wait
    action: wait
    duration: 1s
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "platform is set on suite.yaml") {
		t.Errorf("err = %v, want scenario-platform error", err)
	}
}

func TestLoadSuite_RejectsUndeclaredTargetRef(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
targets:
  devices: [s1]
`,
		"00-bad.yaml": `name: bad
steps:
  - name: x
    action: newtron
    method: GET
    url: /node/{{target.interface}}/x
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "interfaces: dimension") {
		t.Errorf("err = %v, want undeclared-target error", err)
	}
}

func TestLoadSuite_RejectsUndeclaredParamRef(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
parameters:
  declared: foo
`,
		"00-bad.yaml": `name: bad
steps:
  - name: x
    action: newtron
    method: GET
    url: /x
    params:
      value: "{{param.undeclared}}"
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "{{param.undeclared}}") {
		t.Errorf("err = %v, want undeclared-param error", err)
	}
}

func TestLoadSuite_RejectsStepDevicesWithTemplates(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
targets:
  devices: [s1]
`,
		"00-bad.yaml": `name: bad
steps:
  - name: x
    action: newtron
    method: GET
    devices: [s1]
    url: /node/{{target.device}}/x
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "mixes") {
		t.Errorf("err = %v, want mixing-devices error", err)
	}
}

func TestLoadSuite_RejectsDeviceTokenWithTemplates(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
targets:
  devices: [s1]
`,
		"00-bad.yaml": `name: bad
steps:
  - name: x
    action: newtron
    method: GET
    url: /node/{{device}}/x/{{target.device}}
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "{{device}}") {
		t.Errorf("err = %v, want {{device}}-mix error", err)
	}
}

func TestLoadSuite_RejectsUnknownTargetDimension(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
targets:
  unicorns: [u1]
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "unknown target dimension") {
		t.Errorf("err = %v, want unknown-dimension error", err)
	}
}

func TestLoadSuite_RejectsBadTargetValue(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
targets:
  devices: ["s1; rm -rf /"]
`,
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "[A-Za-z0-9_-]+") {
		t.Errorf("err = %v, want whitelist-violation error", err)
	}
}

func TestLoadSuite_RejectsBadParameterDeclaration(t *testing.T) {
	dir := writeSuiteDir(t, map[string]string{
		"suite.yaml": `name: demo
topology: synthetic
parameters:
  bad:
    type: enum
`, // missing values for enum
	})
	_, err := LoadSuite(dir)
	if err == nil || !strings.Contains(err.Error(), "values is required") {
		t.Errorf("err = %v, want enum-values-required error", err)
	}
}
