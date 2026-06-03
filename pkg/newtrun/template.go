package newtrun

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Parameterized scenario template expansion. Tokens {{target.X}} and
// {{param.X}} are substituted per iteration before the step is
// dispatched to its executor. Embedded-target scenarios go through
// their existing {{device}} substitution in steps_newtron.go
// (expandURL); the two paths are disjoint by parser validation —
// parameterized suites may not use {{device}}, embedded-target suites
// may not use {{target.X}} / {{param.X}}.
//
// Substitution is context-aware. Each callsite knows whether it lands
// in a URL path component, a shell command, a JQ expression, a JSON
// value, or free-form text; the engine encodes the value accordingly:
//
//	URL path        url.PathEscape on every substituted value
//	Shell command   single-quote wrap with internal-quote escaping
//	JQ expression   int/bool emitted as literal; string emitted as
//	                JSON-quoted form (do NOT add quotes yourself)
//	JSON params     full-token preserves typed value; partial-token
//	                stringifies; result is later marshaled with Go's
//	                json.Encoder so JSON escaping is automatic
//	Free-form text  no escaping (Expect.Contains, descriptions)
//
// Target values pass an identifier whitelist at parse/override time,
// so target tokens need no escape beyond what the context demands
// defensively. Parameter values arrive typed (string/int/bool/enum/
// ipv4/cidr) from the suite's ParameterSpec.Coerce path; the engine
// uses the Go type to decide how to render.

var templateTokenRe = regexp.MustCompile(`\{\{(target|param)\.([a-zA-Z0-9_]+)\}\}`)

var deviceTokenRe = regexp.MustCompile(`\{\{device\}\}`)

// fullTokenRe matches a string that is ENTIRELY a single template
// token, no surrounding text. Used by JSON params substitution: a
// string value `"{{param.mtu}}"` in YAML decodes to that literal
// string; full-token replacement substitutes the typed Go value
// (e.g., int 9100) so JSON marshal emits `9100`, not `"9100"`.
var fullTokenRe = regexp.MustCompile(`^\{\{(target|param)\.([a-zA-Z0-9_]+)\}\}$`)

// subContext selects the encoding strategy for substituted values.
type subContext int

const (
	ctxURL      subContext = iota // URL path component — url.PathEscape
	ctxURLQuery                   // URL query-string value — url.QueryEscape (escapes & = + that PathEscape leaves alone)
	ctxShell                      // shell argument — single-quote wrap
	ctxJQ                         // JQ expression — JSON-encode strings, literal numerics
	ctxRaw                        // free-form text — no escaping
)

// ExpandStep returns a copy of step with {{target.X}} and {{param.X}}
// references replaced from the supplied bindings. The original step
// is not modified — important for Repeat correctness, where each
// iteration must get a fresh expansion from the same source step.
//
// Each field is expanded under the substitution context appropriate
// to its downstream consumer. Substitution errors (undefined token
// references) abort with a descriptive error; the parser should have
// caught these at suite-load time.
func ExpandStep(step Step, target map[string]string, params map[string]any) (Step, error) {
	expanded := step
	var err error
	expanded.URL, err = applyTemplateURL(step.URL, target, params)
	if err != nil {
		return expanded, fmt.Errorf("url: %w", err)
	}
	// host-exec sends Command through an SSH shell (`sh -c '<cmd>'`),
	// so substituted values must be shell-quoted to survive
	// re-tokenization. newtron-cli execs Command as argv via
	// strings.Fields without a shell — shell-quoting there would
	// place literal single quotes inside the argv elements, breaking
	// the subprocess invocation. Pick the encoding context per
	// action.
	cmdCtx := ctxShell
	if step.Action == ActionNewtronCLI {
		cmdCtx = ctxRaw
	}
	expanded.Command, err = applyTemplate(step.Command, target, params, cmdCtx)
	if err != nil {
		return expanded, fmt.Errorf("command: %w", err)
	}
	expanded.Params, err = expandMapAny(step.Params, target, params)
	if err != nil {
		return expanded, fmt.Errorf("params: %w", err)
	}
	if len(step.Batch) > 0 {
		expanded.Batch = make([]BatchCall, len(step.Batch))
		for i, bc := range step.Batch {
			expanded.Batch[i] = bc
			expanded.Batch[i].URL, err = applyTemplateURL(bc.URL, target, params)
			if err != nil {
				return expanded, fmt.Errorf("batch[%d].url: %w", i, err)
			}
			expanded.Batch[i].Params, err = expandMapAny(bc.Params, target, params)
			if err != nil {
				return expanded, fmt.Errorf("batch[%d].params: %w", i, err)
			}
		}
	}
	if step.Expect != nil {
		expanded.Expect = &ExpectBlock{}
		*expanded.Expect = *step.Expect
		expanded.Expect.JQ, err = applyTemplate(step.Expect.JQ, target, params, ctxJQ)
		if err != nil {
			return expanded, fmt.Errorf("expect.jq: %w", err)
		}
		expanded.Expect.Contains, err = applyTemplate(step.Expect.Contains, target, params, ctxRaw)
		if err != nil {
			return expanded, fmt.Errorf("expect.contains: %w", err)
		}
	}
	return expanded, nil
}

// applyTemplateURL substitutes templates in a URL string. Values
// that land in the path portion are url.PathEscape'd; values that
// land in the query string (anything after the first '?') are
// url.QueryEscape'd. The split matters because PathEscape leaves
// '&', '=' and '+' unescaped — a string parameter containing
// "&evil=1" would silently inject extra query parameters when used
// in a query position, and a literal '+' would be read by the
// server as a space.
func applyTemplateURL(s string, target map[string]string, params map[string]any) (string, error) {
	qIdx := strings.IndexByte(s, '?')
	if qIdx < 0 {
		return applyTemplate(s, target, params, ctxURL)
	}
	pathPart, err := applyTemplate(s[:qIdx], target, params, ctxURL)
	if err != nil {
		return "", err
	}
	queryPart, err := applyTemplate(s[qIdx:], target, params, ctxURLQuery)
	if err != nil {
		return "", err
	}
	return pathPart + queryPart, nil
}

// applyTemplate substitutes {{target.X}} / {{param.X}} tokens in s.
// Each substituted value is encoded for the supplied context.
func applyTemplate(s string, target map[string]string, params map[string]any, ctx subContext) (string, error) {
	if s == "" {
		return "", nil
	}
	var firstErr error
	out := templateTokenRe.ReplaceAllStringFunc(s, func(m string) string {
		if firstErr != nil {
			return m
		}
		sub := templateTokenRe.FindStringSubmatch(m)
		kind, name := sub[1], sub[2]
		var raw any
		switch kind {
		case "target":
			v, ok := target[name]
			if !ok {
				firstErr = fmt.Errorf("undefined target reference %s", m)
				return m
			}
			raw = v
		case "param":
			v, ok := params[name]
			if !ok {
				firstErr = fmt.Errorf("undefined param reference %s", m)
				return m
			}
			raw = v
		}
		return encodeForContext(raw, ctx)
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// encodeForContext renders a typed Go value as a string suitable for
// embedding in the supplied substitution context. See the doc comment
// at the top of this file for the rules.
func encodeForContext(v any, ctx subContext) string {
	s := stringifyScalar(v)
	switch ctx {
	case ctxURL:
		return url.PathEscape(s)
	case ctxURLQuery:
		return url.QueryEscape(s)
	case ctxShell:
		return shellQuote(s)
	case ctxJQ:
		// Numeric / bool types render as JQ literals; strings render
		// as JSON-quoted form (which IS a JQ string literal). Users
		// must not put their own quotes around a {{param.X}} in JQ —
		// the engine emits the quotes.
		switch v.(type) {
		case int, int64, float64, bool:
			return s
		}
		return jsonQuote(s)
	case ctxRaw:
		return s
	default:
		return s
	}
}

// stringifyScalar renders a typed Go value as its plain string form.
// Used by encodeForContext as the pre-escape representation.
func stringifyScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// jsonQuote returns s as a JSON-quoted string literal (including the
// surrounding double quotes). Used to embed a value as a JQ string
// literal — JQ accepts the JSON string grammar inside its expressions.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// expandMapAny walks a JSON-marshalable map and substitutes templates
// in every string value. A string value that is ENTIRELY a single
// {{target.X}} / {{param.X}} token gets the typed Go value (so an
// int parameter stays an int through json.Marshal); other strings
// get inline substitution under ctxRaw (the eventual json.Marshal
// adds JSON escaping). Nested maps and slices recurse.
func expandMapAny(in map[string]any, target map[string]string, params map[string]any) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		ev, err := expandAny(v, target, params)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = ev
	}
	return out, nil
}

func expandAny(v any, target map[string]string, params map[string]any) (any, error) {
	switch t := v.(type) {
	case string:
		if m := fullTokenRe.FindStringSubmatch(t); m != nil {
			kind, name := m[1], m[2]
			switch kind {
			case "target":
				val, ok := target[name]
				if !ok {
					return nil, fmt.Errorf("undefined target reference %s", t)
				}
				return val, nil
			case "param":
				val, ok := params[name]
				if !ok {
					return nil, fmt.Errorf("undefined param reference %s", t)
				}
				return val, nil
			}
		}
		return applyTemplate(t, target, params, ctxRaw)
	case map[string]any:
		return expandMapAny(t, target, params)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			ev, err := expandAny(item, target, params)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = ev
		}
		return out, nil
	default:
		return v, nil
	}
}

// CollectTemplateReferences walks the step and returns every distinct
// {{target.X}} and {{param.X}} key referenced, plus whether the literal
// {{device}} token appears anywhere. The parser uses this to validate
// that all references have declarations and to reject {{device}} in
// parameterized suites.
func CollectTemplateReferences(step Step) (targets, params []string, hasDevice bool) {
	r := &refCollector{seen: map[string]bool{}}
	r.scan(step.URL)
	r.scan(step.Command)
	r.collectFromAny(step.Params)
	for _, bc := range step.Batch {
		r.scan(bc.URL)
		r.collectFromAny(bc.Params)
	}
	if step.Expect != nil {
		r.scan(step.Expect.JQ)
		r.scan(step.Expect.Contains)
	}
	return r.targets, r.params, r.hasDevice
}

type refCollector struct {
	seen      map[string]bool
	targets   []string
	params    []string
	hasDevice bool
}

func (r *refCollector) scan(s string) {
	if s == "" {
		return
	}
	if deviceTokenRe.MatchString(s) {
		r.hasDevice = true
	}
	for _, m := range templateTokenRe.FindAllStringSubmatch(s, -1) {
		kind, name := m[1], m[2]
		key := kind + "." + name
		if r.seen[key] {
			continue
		}
		r.seen[key] = true
		switch kind {
		case "target":
			r.targets = append(r.targets, name)
		case "param":
			r.params = append(r.params, name)
		}
	}
}

func (r *refCollector) collectFromAny(v any) {
	switch t := v.(type) {
	case string:
		r.scan(t)
	case map[string]any:
		for _, vv := range t {
			r.collectFromAny(vv)
		}
	case []any:
		for _, item := range t {
			r.collectFromAny(item)
		}
	}
}
