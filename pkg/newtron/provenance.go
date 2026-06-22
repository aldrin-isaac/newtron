package newtron

import (
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// Spec-kind tokens for DeriveSpecRef — the canonical names of the network spec
// kinds whose application produces intent records. Same vocabulary as the
// OverridableSpecs maps and the CLI nouns.
const (
	SpecKindService = "service"
	SpecKindIPVPN   = "ipvpn"
	SpecKindMACVPN  = "macvpn"
	SpecKindQoS     = "qos"
)

// DeriveSpecRef maps an intent/topology step to the named network spec it was
// projected from — the (kind, name) of the source spec — or ("", "") when the
// step is not the instantiation of a named spec (device/VLAN/VRF primitives,
// raw ACL rules, etc.).
//
// It exists so clients (the newtron CLI, newtcon) get spec provenance from the
// API without re-implementing the per-operation mapping of "which param holds
// the spec name." That mapping is newtron's internal knowledge and drifts as
// operations evolve; centralizing it here (surfaced on TopologyStep by Tree())
// keeps it in one place — §28/§30.
//
// url is the step URL (op is its last path segment, for both "/op" and
// "/interfaces/{name}/{op}" forms); params are the step params, where the spec
// name lives under the operation's step-param key ("service", "ipvpn",
// "macvpn", "policy").
//
// Scope: the unambiguous spec→intent bindings (service, IP-VPN, MAC-VPN, QoS).
// Filter→ACL, route-policy, and prefix-list use content-hashed / service-
// embedded naming where the source spec name is not cleanly recoverable from
// the step; those return ("", "") for now rather than emit a misleading name.
func DeriveSpecRef(url string, params map[string]any) (specKind, specName string) {
	op := url
	if i := strings.LastIndex(op, "/"); i >= 0 {
		op = op[i+1:]
	}

	var kind, paramKey string
	switch op {
	case sonic.OpApplyService, sonic.OpDeployService:
		kind, paramKey = SpecKindService, "service"
	case sonic.OpBindIPVPN:
		kind, paramKey = SpecKindIPVPN, "ipvpn"
	case sonic.OpBindMACVPN:
		kind, paramKey = SpecKindMACVPN, "macvpn"
	case sonic.OpBindQoS:
		kind, paramKey = SpecKindQoS, "policy"
	default:
		return "", ""
	}

	name, _ := params[paramKey].(string)
	if name == "" {
		// The op identifies a spec kind, but the name param is absent — don't
		// claim a kind without a name; that would be a half-record.
		return "", ""
	}
	return kind, name
}
