package spec

// nameIdentifierField returns the synthetic `name` identifier field used
// by every top-level kind. The name lives in the create-X request body,
// not on the spec struct, so universal UIs need it injected as a form
// field by the schema endpoint.
//
// The pattern matches util.NormalizeName's accepted shape — letters,
// digits, underscore, hyphen — so a UI that validates against this
// pattern matches what newtron normalizes server-side. `immutable:true`
// tells UIs to suppress the edit affordance in update-mode forms (newtron
// has no rename verb on top-level kinds).
func nameIdentifierField() *FieldMeta {
	return &FieldMeta{
		Name:        "name",
		Label:       "Name",
		Description: "Unique identifier within this kind. Letters, digits, underscore, and hyphen only. Immutable after creation.",
		Type:        "string",
		Required:    true,
		Pattern:     "^[A-Za-z0-9_-]+$",
		Immutable:   true,
	}
}

// init registers every spec kind that the schema metadata endpoint exposes.
//
// Each kind carries:
//   - Kind: the Go type name (stable across renames inside the package
//     because `ref:"..."` tags on other types point here)
//   - Label / Description: human vocabulary for UIs
//   - Sample: a zero value used by reflection to read field tags
//   - Identifier: which field is the row's key — "name" for top-level
//     kinds; "seq" / "queue_id" / "prefix" for sub-rules
//   - ParentRef (sub-rules only): the wire field a sub-rule's request
//     body uses to identify its parent
//   - Paths: HTTP path templates for the kind's CRUD verbs
//
// The Paths templates use `{netID}` and (for show endpoints) `{name}` —
// UIs substitute these at request time. Read-only kinds omit
// Create/Update/Delete; sub-rule kinds omit List/Show.
func init() {
	// ====================================================================
	// Top-level kinds — full CRUD
	// ====================================================================
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "ServiceSpec",
		Scoped:          true,
		Label:           "Service",
		Description:     "A reusable template that binds VPN references, routing, filters, and QoS — applied to interfaces.",
		Sample:          ServiceSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/services",
			Show:   "/newtron/v1/networks/{netID}/services/{name}",
			Create: "/newtron/v1/networks/{netID}/create-service",
			Update: "/newtron/v1/networks/{netID}/update-service",
			Delete: "/newtron/v1/networks/{netID}/delete-service",
		},
		// Conditional-required surface — UI evaluates against the form's
		// service_type value and toggles each ref-field's required
		// affordance accordingly. Server's 400-on-missing-required at
		// ApplyService stays as the back-stop; this is pure UX so the
		// operator sees the constraint before submitting.
		//
		//   evpn-irb     → ipvpn + macvpn (overlay L2+L3)
		//   evpn-routed  → ipvpn          (overlay L3 only)
		//   evpn-bridged → macvpn         (overlay L2 only)
		//
		// The local types (irb / bridged / routed) take vpn references at
		// apply time, not via the spec — no conditional required there.
		RequiredWhen: map[string]*RequiredWhen{
			"ipvpn":  {Field: "service_type", In: []any{"evpn-irb", "evpn-routed"}},
			"macvpn": {Field: "service_type", In: []any{"evpn-irb", "evpn-bridged"}},
		},
		// Applicability surface — a field whose predicate is false is NOT
		// relevant to the selected service_type; a schema-driven UI hides or
		// disables it and omits it from the payload. ApplyService is the
		// server-side back-stop for each.
		//
		//   ipvpn    — only the overlay-L3 types name an IP-VPN (VRF/L3VNI).
		//              irb/bridged/routed take no ipvpn; evpn-bridged is L2-only.
		//   macvpn   — every L2-bearing type: the EVPN L2 overlays and the
		//              local irb/bridged (which may name a macvpn for the VLAN,
		//              or take --vlan at apply time). routed/evpn-routed are
		//              pure L3 — no macvpn.
		//   vrf_type — selects shared vs per-interface VRF; meaningful only for
		//              the overlay-L3 types that instantiate a VRF (types.go:
		//              "vrf_type, overlay types only").
		//   routing  — L3-only; bridged / evpn-bridged are pure L2.
		AppliesWhen: map[string]*RequiredWhen{
			"ipvpn":    {Field: "service_type", In: []any{"evpn-irb", "evpn-routed"}},
			"macvpn":   {Field: "service_type", In: []any{"evpn-irb", "evpn-bridged", "irb", "bridged"}},
			"vrf_type": {Field: "service_type", In: []any{"evpn-irb", "evpn-routed"}},
			"routing":  {Field: "service_type", In: []any{"routed", "irb", "evpn-routed", "evpn-irb"}},
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "IPVPNSpec",
		Scoped:          true,
		Label:           "IP-VPN",
		Description:     "A Layer-3 VPN — a VRF + L3VNI + route targets that L3 services attach to.",
		Sample:          IPVPNSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/ipvpns",
			Show:   "/newtron/v1/networks/{netID}/ipvpns/{name}",
			Create: "/newtron/v1/networks/{netID}/create-ipvpn",
			Update: "/newtron/v1/networks/{netID}/update-ipvpn",
			Delete: "/newtron/v1/networks/{netID}/delete-ipvpn",
		},
		// The on-device SONiC VRF name is derived from the IP-VPN name,
		// not authored — surfaced read-only so UIs can show it. The "Vrf"
		// prefix is required by sonic-vrf.yang (RCA-044).
		ComputedFields: []FieldMeta{{
			Name:        "vrf_name",
			Label:       "VRF Name",
			Description: "On-device SONiC VRF name, derived as \"Vrf_\"+name (read-only). E.g. IP-VPN \"IRB\" → VRF \"Vrf_IRB\".",
			Type:        "string",
			ReadOnly:    true,
		}},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "MACVPNSpec",
		Scoped:          true,
		Label:           "MAC-VPN",
		Description:     "A Layer-2 VPN — a VLAN + L2VNI + (optional) anycast gateway that L2 services attach to.",
		Sample:          MACVPNSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/macvpns",
			Show:   "/newtron/v1/networks/{netID}/macvpns/{name}",
			Create: "/newtron/v1/networks/{netID}/create-macvpn",
			Update: "/newtron/v1/networks/{netID}/update-macvpn",
			Delete: "/newtron/v1/networks/{netID}/delete-macvpn",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "QoSPolicy",
		Scoped:          true,
		Label:           "QoS Policy",
		Description:     "A declarative queue policy — strict / DWRR scheduling, DSCP mapping, optional ECN.",
		Sample:          QoSPolicy{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/qos-policies",
			Show:   "/newtron/v1/networks/{netID}/qos-policies/{name}",
			Create: "/newtron/v1/networks/{netID}/create-qos-policy",
			Update: "/newtron/v1/networks/{netID}/update-qos-policy",
			Delete: "/newtron/v1/networks/{netID}/delete-qos-policy",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "FilterSpec",
		Scoped:          true,
		Label:           "Filter",
		Description:     "A reusable ACL — an ordered list of permit/deny rules.",
		Sample:          FilterSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/filters",
			Show:   "/newtron/v1/networks/{netID}/filters/{name}",
			Create: "/newtron/v1/networks/{netID}/create-filter",
			Update: "/newtron/v1/networks/{netID}/update-filter",
			Delete: "/newtron/v1/networks/{netID}/delete-filter",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "RoutePolicy",
		Scoped:          true,
		Label:           "Route Policy",
		Description:     "A reusable BGP route-policy — an ordered list of match-and-set rules.",
		Sample:          RoutePolicy{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/route-policies",
			Show:   "/newtron/v1/networks/{netID}/route-policies/{name}",
			Create: "/newtron/v1/networks/{netID}/create-route-policy",
			Update: "/newtron/v1/networks/{netID}/update-route-policy",
			Delete: "/newtron/v1/networks/{netID}/delete-route-policy",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "NodeSpec",
		Label:           "Node",
		Description:     "Per-node specification — management IP, loopback IP, zone, optional EVPN peering, and per-node overrides.",
		Sample:          NodeSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/nodes",
			Show:   "/newtron/v1/networks/{netID}/nodes/{name}",
			Create: "/newtron/v1/networks/{netID}/create-node",
			Update: "/newtron/v1/networks/{netID}/update-node",
			Delete: "/newtron/v1/networks/{netID}/delete-node",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "ZoneSpec",
		Label:           "Zone",
		Description:     "A zone — a scope for spec overrides between network-global and per-device.",
		Sample:          ZoneSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/zones",
			Show:   "/newtron/v1/networks/{netID}/zones/{name}",
			Create: "/newtron/v1/networks/{netID}/create-zone",
			Update: "/newtron/v1/networks/{netID}/update-zone",
			Delete: "/newtron/v1/networks/{netID}/delete-zone",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "PrefixListSpec",
		Scoped:          true,
		Label:           "Prefix List",
		Description:     "A reusable list of CIDR prefixes referenced by filter rules and route policies.",
		Sample:          PrefixListSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/prefix-lists",
			Show:   "/newtron/v1/networks/{netID}/prefix-lists/{name}",
			Create: "/newtron/v1/networks/{netID}/create-prefix-list",
			Update: "/newtron/v1/networks/{netID}/update-prefix-list",
			Delete: "/newtron/v1/networks/{netID}/delete-prefix-list",
		},
	})

	// ====================================================================
	// Read-only top-level kind — Platform support is deeply tied to the
	// backend (HWSKU, port stride, SAI compatibility); operators don't
	// author platforms via a universal UI. The schema is exposed so UIs
	// can render a read-only platform view; Create/Update/Delete paths
	// are absent.
	// ====================================================================
	RegisterSchemaKind(SchemaRegistration{
		Kind:            "PlatformSpec",
		Label:           "Platform",
		Description:     "Hardware platform definition (HWSKU, ports, VM image, feature support). Read-only via the universal UI — authoring requires deep backend coordination.",
		Sample:          PlatformSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List: "/newtron/v1/networks/{netID}/platforms",
			Show: "/newtron/v1/networks/{netID}/platforms/{name}",
		},
	})

	// ====================================================================
	// Sub-rule kinds — addressed via their parent's name in the request
	// body. No List/Show (they live under the parent's detail), no Show.
	// ====================================================================
	queueIDMin := 0
	queueIDMax := 7
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "QoSQueue",
		Scoped:      true,
		Label:       "QoS Queue",
		Description: "One queue (traffic class) within a QoS policy.",
		Sample:      QoSQueue{},
		Identifier:  "queue_id",
		// queue_id is the slot index in QoSPolicy.Queues — implicit in
		// the parent's array, not a field on the QoSQueue struct. We
		// inject it as a synthetic form field so universal UIs render
		// the complete form shape.
		IdentifierField: &FieldMeta{
			Name:        "queue_id",
			Label:       "Queue ID",
			Description: "Slot index 0–7 — selects the queue's position in the policy's queue array. Immutable after creation.",
			Type:        "int",
			Required:    true,
			Min:         &queueIDMin,
			Max:         &queueIDMax,
			Immutable:   true,
		},
		ParentRef: "policy",
		Paths: SchemaPaths{
			Create: "/newtron/v1/networks/{netID}/add-qos-queue",
			Update: "/newtron/v1/networks/{netID}/update-qos-queue",
			Delete: "/newtron/v1/networks/{netID}/remove-qos-queue",
		},
		// weight is a DWRR scheduling parameter; strict-priority queues take
		// no weight (loader rejects a non-zero weight on a strict queue). A
		// schema-driven UI hides weight unless type == dwrr.
		AppliesWhen: map[string]*RequiredWhen{
			"weight": {Field: "type", In: []any{"dwrr"}},
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "FilterRule",
		Scoped:      true,
		Label:       "Filter Rule",
		Description: "One match-and-action rule inside a filter.",
		Sample:      FilterRule{},
		Identifier:  "seq",
		ParentRef:   "filter",
		Paths: SchemaPaths{
			Create: "/newtron/v1/networks/{netID}/add-filter-rule",
			Update: "/newtron/v1/networks/{netID}/update-filter-rule",
			Delete: "/newtron/v1/networks/{netID}/remove-filter-rule",
		},
	})
	// PortConfig has no dedicated CRUD verbs — unlike the other sub-rule kinds
	// it is authored under a topology device's `ports` map and persisted via the
	// topology-device update (PUT /topology/nodes/{name}). The schema kind exists
	// to give a universal UI the config form; the operator picks the port name
	// from the platform's `ports` inventory (the immutable identifier here).
	// Not Scoped: port config is authored on a concrete topology device, not at
	// network/zone/node scope (it is not an overridable spec), so it takes no
	// scope/scope_instance discriminators.
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "PortConfig",
		Label:       "Port Config",
		Description: "Per-port PORT-table config (admin status, MTU, speed, description) for one physical port on a topology device, keyed by port name. Written via the topology-device update; fields mirror the YANG-derived PORT constraints.",
		Sample:      PortConfig{},
		Identifier:  "port",
		ParentRef:   "device",
		IdentifierField: &FieldMeta{
			Name:        "port",
			Label:       "Port",
			Description: "Device-native port name (e.g. \"Ethernet0\") — chosen from the platform's ports inventory. Immutable; it is the map key.",
			Type:        "string",
			Required:    true,
			Immutable:   true,
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "RoutePolicyRule",
		Scoped:      true,
		Label:       "Route Policy Rule",
		Description: "One match-and-action rule inside a route policy.",
		Sample:      RoutePolicyRule{},
		Identifier:  "seq",
		ParentRef:   "policy",
		Paths: SchemaPaths{
			Create: "/newtron/v1/networks/{netID}/add-route-policy-rule",
			Update: "/newtron/v1/networks/{netID}/update-route-policy-rule",
			Delete: "/newtron/v1/networks/{netID}/remove-route-policy-rule",
		},
	})
	// PrefixListEntry has no Go struct — PrefixLists is map[string][]string.
	// The schema describes the form shape (a single `prefix` field); the
	// wire shape on add/remove is `{prefix_list, prefix}` — the ParentRef
	// tells the UI to include `prefix_list: <parent>` in the body.
	// Per §47 (CONFIG_DB Composite Key Is the Identity, extended to
	// spec-side identity) the prefix IS the entry — no Update verb.
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "PrefixListEntry",
		Scoped:      true,
		Label:       "Prefix List Entry",
		Description: "One CIDR prefix inside a prefix list.",
		Sample:      PrefixListEntry{},
		Identifier:  "prefix",
		ParentRef:   "prefix_list",
		Paths: SchemaPaths{
			Create: "/newtron/v1/networks/{netID}/add-prefix-list-entry",
			Delete: "/newtron/v1/networks/{netID}/remove-prefix-list-entry",
		},
	})

	// ====================================================================
	// Embedded-object kinds — UIs recurse into these via item_kind on the
	// parent's field; they have no path entries because they're never
	// addressed independently.
	// ====================================================================
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "RoutingSpec",
		Label:       "Service Routing",
		Description: "BGP / static routing parameters embedded on a service.",
		Sample:      RoutingSpec{},
		// BGP-only fields are not applicable when protocol=static. A
		// schema-driven UI hides/disables them and omits them from the
		// payload; the apply path ignores them server-side regardless
		// (a static service never reads peer_as / policies / communities /
		// prefix-lists). protocol and redistribute apply to both protocols
		// and carry no predicate.
		AppliesWhen: map[string]*RequiredWhen{
			"peer_as":            {Field: "protocol", Equals: "bgp"},
			"import_policy":      {Field: "protocol", Equals: "bgp"},
			"export_policy":      {Field: "protocol", Equals: "bgp"},
			"import_community":   {Field: "protocol", Equals: "bgp"},
			"export_community":   {Field: "protocol", Equals: "bgp"},
			"import_prefix_list": {Field: "protocol", Equals: "bgp"},
			"export_prefix_list": {Field: "protocol", Equals: "bgp"},
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "RoutePolicySet",
		Label:       "Route Policy Set Actions",
		Description: "Attributes (LOCAL_PREF, community, MED) applied to permitted routes — embedded on a route-policy rule.",
		Sample:      RoutePolicySet{},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "EVPNConfig",
		Label:       "EVPN Overlay Peering",
		Description: "Per-node EVPN BGP overlay peering — embedded on a node.",
		Sample:      EVPNConfig{},
	})
}
