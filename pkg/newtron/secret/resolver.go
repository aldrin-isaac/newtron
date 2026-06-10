package secret

import (
	"fmt"
	"strings"
)

// refPrefix is the marker that signals a value is a reference into
// the secret store rather than a literal. Verbatim from
// auth-design.md L0; the choice is intentional:
//
//   - Shell-style ${...} reads as a substitution to any operator
//     who has used bash, Make, or compose.yml — no separate doc
//     section needed to introduce the syntax.
//   - The "secret:" namespace within ${...} lets the same shape
//     extend to other resolved-at-load substitutions later if any
//     ever appear (none planned for L0).
//   - The prefix is unlikely to collide with real password
//     content. The shipped 58 plaintext password values verified by
//     the L0 survey contain no instances of "${secret:".
const refPrefix = "${secret:"

// refSuffix closes the substitution form. See refPrefix.
const refSuffix = "}"

// IsRef reports whether s is a secret-store reference. Used by the
// spec loader to decide whether to consult the store for a given
// field value. Bare literal values (the current behavior for
// plaintext passwords) return false and pass through unchanged.
func IsRef(s string) bool {
	return strings.HasPrefix(s, refPrefix) && strings.HasSuffix(s, refSuffix)
}

// Resolve returns the literal value for s. When s is a reference of
// the form "${secret:KEY}", the store is consulted; the returned
// string is the looked-up value. When s is not a reference, it is
// returned unchanged — plaintext spec values keep working without a
// store configured.
//
// A reference combined with a nil store is an error: a spec opted
// into the new path, but the deployment didn't wire a store, and
// silently falling back to a literal "${secret:KEY}" (which would
// then be sent as a password to the device) would be a security
// regression.
//
// A reference with a key not in the store is also an error,
// surfacing the missing key by name so the operator can `bin/newtron
// secrets put` to fix it.
func Resolve(s string, store Store) (string, error) {
	if !IsRef(s) {
		return s, nil
	}
	if store == nil {
		return "", fmt.Errorf("secret: reference %q present in spec but no --secret-store is configured", s)
	}
	key := s[len(refPrefix) : len(s)-len(refSuffix)]
	if key == "" {
		return "", fmt.Errorf("secret: empty key in reference %q", s)
	}
	v, err := store.Get(key)
	if err != nil {
		return "", err
	}
	return v, nil
}
