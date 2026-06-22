package spec

import "fmt"

// NotFoundError reports that a named spec — a service, IP-VPN, MAC-VPN, QoS
// policy, filter, route policy, or prefix list — was not present in the loaded
// specs. Returned by the name-keyed spec accessors (getSpec).
//
// It is a distinct, detectable type (via errors.As) for one reason beyond a
// readable message: reconstruction must recognize an intent that references a
// spec which has since been removed or renamed — an *orphaned intent* — and
// skip it rather than failing the entire device. See DESIGN_PRINCIPLES_NEWTRON
// §5/§20/§21. Spec-resolution sites on the replay path must preserve this type
// through wrapping (%w) so the classification survives.
type NotFoundError struct {
	Kind string // spec kind, e.g. "ipvpn", "service", "macvpn"
	Name string // the (canonical) name that did not resolve
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s '%s' not found", e.Kind, e.Name)
}
