package newtrun

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// Suite is a collection of scenarios that share a topology and (when
// the suite is parameterized) a single targets/parameters block.
// Suites live on disk as a directory: newtrun/suites/<name>/, with a
// suite.yaml declaring the suite metadata + targets/parameters, and
// NN-<name>.yaml scenario files alongside it.
//
// A suite is the unit of orchestration: the inline-compose-and-run
// endpoint accepts a suite name plus optional per-key overrides of
// targets and parameters; every scenario in the suite runs under the
// same target/parameter bindings. This is why Targets and Parameters
// belong here, not on Scenario — they describe the run, not the
// template.
type Suite struct {
	Name        string                   `yaml:"name"`
	Description string                   `yaml:"description,omitempty"`
	Topology    string                   `yaml:"topology"`
	Platform    string                   `yaml:"platform,omitempty"`
	Targets     map[string][]string      `yaml:"targets,omitempty"`
	Parameters  map[string]ParameterSpec `yaml:"parameters,omitempty"`

	// Scenarios is the dependency-ordered list of scenarios loaded
	// from the suite directory. Populated by LoadSuite. Not part of
	// the wire format — scenarios are separate YAML files.
	Scenarios []*Scenario `yaml:"-"`
}

// IsParameterized reports whether the suite declares targets or
// parameters. Embedded-target suites have neither; their scenarios
// use step-level devices: selectors and {{device}} substitution.
func (s *Suite) IsParameterized() bool {
	return len(s.Targets) > 0 || len(s.Parameters) > 0
}

// TargetIterations returns the cross-product expansion of the target
// dimensions, with singular keys (`devices` → `device`,
// `interfaces` → `interface`). For embedded-target suites (no
// Targets) returns a single nil binding so callers iterate once.
//
// Iteration order is deterministic: dimensions sorted by name, values
// in declaration order.
func (s *Suite) TargetIterations() []map[string]string {
	if len(s.Targets) == 0 {
		return []map[string]string{nil}
	}

	dims := make([]string, 0, len(s.Targets))
	for k := range s.Targets {
		dims = append(dims, k)
	}
	sort.Strings(dims)

	iterations := []map[string]string{{}}
	for _, dim := range dims {
		singular, _ := singularize(dim) // parser already validated
		values := s.Targets[dim]
		next := make([]map[string]string, 0, len(iterations)*len(values))
		for _, prev := range iterations {
			for _, v := range values {
				m := make(map[string]string, len(prev)+1)
				for k, pv := range prev {
					m[k] = pv
				}
				m[singular] = v
				next = append(next, m)
			}
		}
		iterations = next
	}
	return iterations
}

// EffectiveParameters returns the resolved parameter values for a
// run, combining the suite's declared defaults with the supplied
// overrides. Overrides win on key collision. Returns nil when no
// parameters are declared or supplied.
//
// Type-coerced through ParameterSpec.Coerce — overrides come in as
// untyped JSON values; the result holds the typed Go value.
func (s *Suite) EffectiveParameters(overrides map[string]any) (map[string]any, error) {
	if len(s.Parameters) == 0 && len(overrides) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(s.Parameters))
	for name, spec := range s.Parameters {
		raw, hasOverride := overrides[name]
		if !hasOverride {
			if spec.Default == nil {
				if spec.Required {
					return nil, fmt.Errorf("parameter %q is required and has no default", name)
				}
				continue
			}
			out[name] = spec.Default
			continue
		}
		v, err := spec.Coerce(raw)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		out[name] = v
	}
	// Reject overrides that name no declared parameter — surface
	// typos rather than silently ignoring them.
	for name := range overrides {
		if _, ok := s.Parameters[name]; !ok {
			return nil, fmt.Errorf("unknown parameter %q (not declared in suite.yaml)", name)
		}
	}
	return out, nil
}

// EffectiveTargets returns the resolved target dimensions for a run,
// combining the suite's declared targets with per-key overrides.
// Each override key replaces (not merges with) the corresponding
// suite-level list. Returns the suite's declared targets unchanged
// when overrides is empty. Validates that every value is a safe
// identifier ([A-Za-z0-9_-]+) — target values address infrastructure,
// they must not be free-form strings.
func (s *Suite) EffectiveTargets(overrides map[string][]string) (map[string][]string, error) {
	out := make(map[string][]string, len(s.Targets))
	for k, v := range s.Targets {
		out[k] = append([]string{}, v...)
	}
	for k, v := range overrides {
		if _, ok := s.Targets[k]; !ok {
			return nil, fmt.Errorf("unknown target dimension %q (not declared in suite.yaml)", k)
		}
		if len(v) == 0 {
			return nil, fmt.Errorf("target dimension %q: override is empty", k)
		}
		out[k] = append([]string{}, v...)
	}
	for k, values := range out {
		for _, val := range values {
			if !targetValueRe.MatchString(val) {
				return nil, fmt.Errorf("target %q value %q: must match [A-Za-z0-9_-]+ (identifiers only)", k, val)
			}
		}
	}
	return out, nil
}

// targetValueRe is the whitelist for target values. Targets address
// infrastructure (device names, interface names); they are always
// identifiers, never free-form. Enforced at parse time and at
// request-override time so substitution into URL paths and shell
// commands need no further escaping for target tokens.
var targetValueRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// singularizeMap maps recognized plural target dimensions to their
// singular template form. New dimensions must be added here explicitly
// — the suite loader rejects unknown plurals to keep the template
// surface reviewable.
var singularizeMap = map[string]string{
	"devices":    "device",
	"interfaces": "interface",
}

// singularize converts a target dimension key (plural, as declared in
// the YAML) to the singular form used in {{target.X}} templates.
// Returns ok=false for unknown dimensions; the suite loader uses that
// to reject undeclared target keys at parse time.
func singularize(plural string) (string, bool) {
	s, ok := singularizeMap[plural]
	return s, ok
}

// pluralize is the inverse of singularize — given a template-side
// singular (`device`), recover the YAML-side plural (`devices`) for
// use in error messages. Operators see {{target.device}} in their
// templates and add `device: [...]` (singular) to suite.yaml; the
// diagnostic needs to name the form they should actually write.
// Falls back to plural+"s" if no mapping exists, but every key in
// singularizeMap has a reverse entry so the fallback is for
// future-proofing only.
func pluralize(singular string) string {
	for plural, s := range singularizeMap {
		if s == singular {
			return plural
		}
	}
	return singular + "s"
}

// LoadSuite reads a suite directory: suite.yaml declares the suite,
// every other .yaml file is a scenario. Returns the suite with its
// Scenarios slice populated in dependency order.
//
// Validation rules:
//   - suite.yaml must exist and declare name + topology
//   - suite-level targets/parameters must be well-formed (recognized
//     dimensions, non-empty target lists, target values pass the
//     identifier whitelist, parameter declarations satisfy their
//     own ParameterSpec)
//   - scenarios may not declare topology or platform (those are
//     suite-level)
//   - any scenario step that uses {{target.X}} or {{param.X}} opts
//     into parameterization for that step: the reference must
//     resolve to a suite-level declaration, the step must not also
//     set devices: or use {{device}}, and the suite must declare a
//     matching dimension/parameter
func LoadSuite(dir string) (*Suite, error) {
	suitePath := filepath.Join(dir, "suite.yaml")
	data, err := os.ReadFile(suitePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", suitePath, err)
	}
	var suite Suite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", suitePath, err)
	}
	if suite.Name == "" {
		return nil, fmt.Errorf("%s: name is required", suitePath)
	}
	if suite.Topology == "" {
		return nil, fmt.Errorf("%s: topology is required", suitePath)
	}
	if err := validateSuiteDeclaration(&suite); err != nil {
		return nil, fmt.Errorf("%s: %w", suitePath, err)
	}

	scenarios, paths, err := loadScenarioFiles(dir)
	if err != nil {
		return nil, err
	}
	for i, sc := range scenarios {
		path := paths[i]
		if sc.Topology != "" {
			return nil, fmt.Errorf("%s: topology is set on suite.yaml, not on individual scenarios", path)
		}
		if sc.Platform != "" {
			return nil, fmt.Errorf("%s: platform is set on suite.yaml (or CLI flag), not on individual scenarios", path)
		}
		if err := validateScenarioAgainstSuite(sc, &suite, path); err != nil {
			return nil, err
		}
	}

	if HasRequires(scenarios) {
		sorted, err := ValidateDependencyGraph(scenarios)
		if err != nil {
			return nil, err
		}
		scenarios = sorted
	}

	suite.Scenarios = scenarios
	return &suite, nil
}

// validateSuiteDeclaration checks the suite's own targets and
// parameters blocks for internal consistency: known target
// dimensions, non-empty target lists, parameter defaults satisfying
// their declared types.
func validateSuiteDeclaration(s *Suite) error {
	for plural, values := range s.Targets {
		if _, ok := singularize(plural); !ok {
			return fmt.Errorf("unknown target dimension %q (recognized: devices, interfaces)", plural)
		}
		if len(values) == 0 {
			return fmt.Errorf("target dimension %q is empty", plural)
		}
		for _, v := range values {
			if !targetValueRe.MatchString(v) {
				return fmt.Errorf("target %q value %q: must match [A-Za-z0-9_-]+", plural, v)
			}
		}
	}
	for name, spec := range s.Parameters {
		if err := spec.ValidateDeclaration(); err != nil {
			return fmt.Errorf("parameter %q: %w", name, err)
		}
	}
	return nil
}

// validateScenarioAgainstSuite checks template references per
// scenario. A scenario opts into parameterized expansion by using
// {{target.X}} or {{param.X}} tokens; once it does, every reference
// must resolve to a suite-level declaration, and the scenario may not
// also use step-level devices: / {{device}} (which belong to embedded-
// target scenarios). Scenarios with no template references are
// embedded-target — free to use step.Devices and {{device}} — and
// coexist in the same suite alongside parameterized scenarios.
func validateScenarioAgainstSuite(sc *Scenario, suite *Suite, path string) error {
	declaredTargets := make(map[string]bool, len(suite.Targets))
	for plural := range suite.Targets {
		singular, _ := singularize(plural)
		declaredTargets[singular] = true
	}

	for i, step := range sc.Steps {
		prefix := fmt.Sprintf("%s step %d (%s)", path, i, step.Name)
		targets, params, hasDevice := CollectTemplateReferences(step)
		scenarioParam := len(targets) > 0 || len(params) > 0

		if scenarioParam {
			if step.Devices.All || len(step.Devices.Devices) > 0 {
				return fmt.Errorf("%s: step mixes {{target.X}}/{{param.X}} with a devices: selector — pick one (parameterized OR embedded-target)", prefix)
			}
			if hasDevice {
				return fmt.Errorf("%s: step mixes {{target.X}}/{{param.X}} with {{device}} — use {{target.device}} instead", prefix)
			}
			for _, t := range targets {
				if !declaredTargets[t] {
					return fmt.Errorf("%s: references {{target.%s}} but suite.yaml has no %s: dimension declared", prefix, t, pluralize(t))
				}
			}
			for _, p := range params {
				if _, ok := suite.Parameters[p]; !ok {
					return fmt.Errorf("%s: references {{param.%s}} but parameter not declared in suite.yaml", prefix, p)
				}
			}
		}
	}
	return nil
}

// ScenarioIsParameterized reports whether any step in the scenario
// uses {{target.X}} or {{param.X}} tokens. Per-scenario flag (not
// suite-wide): an embedded-target scenario can live in a parameterized
// suite without participating in iteration. The runner uses this to
// pick the iteration count for the scenario (one nil binding for
// embedded-target; cross-product of suite targets for parameterized).
func ScenarioIsParameterized(sc *Scenario) bool {
	for _, step := range sc.Steps {
		targets, params, _ := CollectTemplateReferences(step)
		if len(targets) > 0 || len(params) > 0 {
			return true
		}
	}
	return false
}

// ParameterSpec describes a single parameter declared by a suite:
// its type, default, constraints, and required-ness. The YAML
// supports two forms:
//
//   - shorthand scalar: `admin_status: up` infers type=string,
//     default="up"; `mtu: 9100` infers type=int, default=9100;
//     `active: true` infers type=bool, default=true.
//
//   - verbose map: explicit `type:` plus type-specific fields like
//     `values:` (enum), `min:` / `max:` (int), `required:` (any type).
//
// Coerce takes a request-time override (an arbitrary JSON value),
// validates it against the spec, and returns the typed Go value
// to substitute into templates.
type ParameterSpec struct {
	Type     ParameterType `yaml:"type,omitempty"`
	Default  any           `yaml:"default,omitempty"`
	Values   []string      `yaml:"values,omitempty"` // for enum
	Min      *int          `yaml:"min,omitempty"`    // for int
	Max      *int          `yaml:"max,omitempty"`    // for int
	Required bool          `yaml:"required,omitempty"`
}

// ParameterType is the declared type of a parameter. Substitution
// behavior depends on it: int/bool render without quoting in JQ;
// string/enum/ipv4/cidr render quoted; URL path components are always
// URL-escaped regardless of type.
type ParameterType string

const (
	ParameterTypeString ParameterType = "string"
	ParameterTypeInt    ParameterType = "int"
	ParameterTypeBool   ParameterType = "bool"
	ParameterTypeEnum   ParameterType = "enum"
	ParameterTypeIPv4   ParameterType = "ipv4"
	ParameterTypeCIDR   ParameterType = "cidr"
)

// UnmarshalYAML accepts both the shorthand scalar form and the
// verbose map form. Shorthand: the value's YAML kind drives type
// inference (string → string, int → int, bool → bool); the value
// itself becomes Default. Verbose: standard struct decoding.
func (p *ParameterSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err == nil {
			// Tag inspection: !!str → string, !!int → int, !!bool → bool
			switch node.Tag {
			case "!!int":
				var i int
				if err := node.Decode(&i); err != nil {
					return err
				}
				p.Type = ParameterTypeInt
				p.Default = i
				return nil
			case "!!bool":
				var b bool
				if err := node.Decode(&b); err != nil {
					return err
				}
				p.Type = ParameterTypeBool
				p.Default = b
				return nil
			default:
				p.Type = ParameterTypeString
				p.Default = s
				return nil
			}
		}
	}
	// Verbose form: decode into a shadow struct to avoid recursing.
	type shadow ParameterSpec
	var sh shadow
	if err := node.Decode(&sh); err != nil {
		return err
	}
	*p = ParameterSpec(sh)
	if p.Type == "" {
		p.Type = ParameterTypeString
	}
	return nil
}

// ValidateDeclaration checks the spec is internally consistent:
// known type, well-formed constraints, default satisfies the spec
// (when present).
func (p *ParameterSpec) ValidateDeclaration() error {
	switch p.Type {
	case ParameterTypeString, ParameterTypeInt, ParameterTypeBool,
		ParameterTypeEnum, ParameterTypeIPv4, ParameterTypeCIDR:
	case "":
		return fmt.Errorf("type is required")
	default:
		return fmt.Errorf("unknown type %q (recognized: string, int, bool, enum, ipv4, cidr)", p.Type)
	}
	if p.Type == ParameterTypeEnum && len(p.Values) == 0 {
		return fmt.Errorf("enum: values is required")
	}
	if p.Default != nil {
		if _, err := p.Coerce(p.Default); err != nil {
			return fmt.Errorf("default: %w", err)
		}
	}
	return nil
}

// Coerce validates a value against the spec and returns the typed
// Go representation. Used both at declaration time (validating
// the default) and at request time (validating an override).
func (p *ParameterSpec) Coerce(v any) (any, error) {
	switch p.Type {
	case ParameterTypeString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", v)
		}
		return s, nil
	case ParameterTypeInt:
		i, err := coerceInt(v)
		if err != nil {
			return nil, err
		}
		if p.Min != nil && i < *p.Min {
			return nil, fmt.Errorf("value %d below min %d", i, *p.Min)
		}
		if p.Max != nil && i > *p.Max {
			return nil, fmt.Errorf("value %d above max %d", i, *p.Max)
		}
		return i, nil
	case ParameterTypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", v)
		}
		return b, nil
	case ParameterTypeEnum:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected one of %v, got %T", p.Values, v)
		}
		for _, allowed := range p.Values {
			if s == allowed {
				return s, nil
			}
		}
		return nil, fmt.Errorf("value %q not in %v", s, p.Values)
	case ParameterTypeIPv4:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected IPv4 string, got %T", v)
		}
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("not a valid IPv4 address: %q", s)
		}
		return s, nil
	case ParameterTypeCIDR:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("expected IPv4 CIDR string, got %T", v)
		}
		ip, _, err := net.ParseCIDR(s)
		if err != nil || ip.To4() == nil {
			return nil, fmt.Errorf("not a valid IPv4 CIDR: %q", s)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("unknown type %q", p.Type)
	}
}

// coerceInt accepts the various forms an int can arrive in from
// JSON / YAML decoding (int, int64, float64 with no fractional part).
func coerceInt(v any) (int, error) {
	switch t := v.(type) {
	case int:
		return t, nil
	case int64:
		return int(t), nil
	case float64:
		if t != float64(int(t)) {
			return 0, fmt.Errorf("expected int, got fractional %v", t)
		}
		return int(t), nil
	default:
		return 0, fmt.Errorf("expected int, got %T", v)
	}
}
