package newtron

import (
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// Spec-kind tokens for DeriveSpecRef — the canonical names of the network spec
// kinds whose application produces intent records. Same vocabulary as the
// OverridableSpecs maps and the CLI nouns.
const (
	SpecKindService = "service"
	SpecKindIPVPN   = "ipvpn"
	SpecKindMACVPN  = "macvpn"
	SpecKindQoS     = "qos"
	SpecKindFilter  = "filter"
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
// Scope: service, IP-VPN, MAC-VPN, QoS — and filter, for service-derived ACLs.
// A service-derived ACL is content-hash-named (§24/§25), so the name can't be
// reversed to its filter; instead the generator records the source filter name
// in sonic.FieldFilter, which is read here. A standalone/raw create-acl has no
// source filter and returns ("", ""). Route-policy and prefix-list never appear
// as standalone steps (they are sub-resources of a service application, tracked
// on the service intent), so there is no step to attribute them to — the
// enclosing service step already carries spec_kind=service.
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
	case sonic.OpCreateACL:
		// Only service-derived ACLs carry the source filter name (sonic.FieldFilter);
		// a standalone/raw create-acl has no filter param and falls through to
		// ("", "") via the name-absent guard below.
		kind, paramKey = SpecKindFilter, "filter"
	default:
		return "", ""
	}

	name, _ := params[paramKey].(string)
	if name == "" {
		// The op identifies a spec kind, but the name param is absent — don't
		// claim a kind without a name; that would be a half-record.
		return "", ""
	}
	// Return the spec's CANONICAL identity, not the raw step value. Topology
	// steps store the name in whatever casing the operator typed at apply time
	// (e.g. "transit", "local-irb"); the spec it references is keyed canonically
	// (§36 normalizes at load: "TRANSIT", "LOCAL_IRB"). Normalizing here makes
	// spec_name equal the GET /services / /ipvpns key exactly, so a client
	// matches provenance against the spec surface with no canonicalization of
	// its own.
	return kind, util.NormalizeName(name)
}
