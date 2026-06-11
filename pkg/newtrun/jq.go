package newtrun

import (
	"encoding/json"
	"fmt"

	"github.com/itchyny/gojq"
)

// runJQ parses jqExpr, runs it against the JSON in raw, and returns
// the first value the expression produces. The two newtrun call
// sites have different post-extraction contracts — evalJQ asserts
// the value is boolean true, applyCaptures writes it under a
// variable name in the captured map — but both go through this
// shared parse → decode → run → first-result → error-type-check
// sequence. Keeping the parse-and-decode plumbing here avoids
// divergence in how the two paths handle malformed JSON, malformed
// expressions, or gojq's own error returns.
//
// Returns an error when the expression fails to parse, raw is
// non-empty but isn't valid JSON, the expression evaluates to a
// gojq runtime error, or the expression produces no output (which
// gojq reports by yielding nothing rather than by returning an
// error from Next).
func runJQ(jqExpr string, raw json.RawMessage) (any, error) {
	query, err := gojq.Parse(jqExpr)
	if err != nil {
		return nil, fmt.Errorf("jq parse error: %w", err)
	}
	var input any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}
	iter := query.Run(input)
	v, ok := iter.Next()
	if !ok {
		return nil, fmt.Errorf("expression produced no output")
	}
	if e, isErr := v.(error); isErr {
		return nil, fmt.Errorf("jq eval: %w", e)
	}
	return v, nil
}
