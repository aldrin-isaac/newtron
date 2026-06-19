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
		Kind:        "ServiceSpec",
		Label:       "Service",
		Description: "A reusable template that binds VPN references, routing, filters, and QoS — applied to interfaces.",
		Sample:      ServiceSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/services",
			Show:   "/newtron/v1/networks/{netID}/services/{name}",
			Create: "/newtron/v1/networks/{netID}/create-service",
			Update: "/newtron/v1/networks/{netID}/update-service",
			Delete: "/newtron/v1/networks/{netID}/delete-service",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "IPVPNSpec",
		Label:       "IP-VPN",
		Description: "A Layer-3 VPN — a VRF + L3VNI + route targets that L3 services attach to.",
		Sample:      IPVPNSpec{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/ipvpns",
			Show:   "/newtron/v1/networks/{netID}/ipvpns/{name}",
			Create: "/newtron/v1/networks/{netID}/create-ipvpn",
			Update: "/newtron/v1/networks/{netID}/update-ipvpn",
			Delete: "/newtron/v1/networks/{netID}/delete-ipvpn",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "MACVPNSpec",
		Label:       "MAC-VPN",
		Description: "A Layer-2 VPN — a VLAN + L2VNI + (optional) anycast gateway that L2 services attach to.",
		Sample:      MACVPNSpec{},
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
		Kind:        "QoSPolicy",
		Label:       "QoS Policy",
		Description: "A declarative queue policy — strict / DWRR scheduling, DSCP mapping, optional ECN.",
		Sample:      QoSPolicy{},
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
		Kind:        "FilterSpec",
		Label:       "Filter",
		Description: "A reusable ACL — an ordered list of permit/deny rules.",
		Sample:      FilterSpec{},
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
		Kind:        "RoutePolicy",
		Label:       "Route Policy",
		Description: "A reusable BGP route-policy — an ordered list of match-and-set rules.",
		Sample:      RoutePolicy{},
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
		Kind:        "DeviceProfile",
		Label:       "Device Profile",
		Description: "Per-device specification — management IP, loopback IP, zone, optional EVPN peering, and per-device overrides.",
		Sample:      DeviceProfile{},
		Identifier:      "name",
		IdentifierField: nameIdentifierField(),
		Paths: SchemaPaths{
			List:   "/newtron/v1/networks/{netID}/profiles",
			Show:   "/newtron/v1/networks/{netID}/nodes/{name}",
			Create: "/newtron/v1/networks/{netID}/create-profile",
			Update: "/newtron/v1/networks/{netID}/update-profile",
			Delete: "/newtron/v1/networks/{netID}/delete-profile",
		},
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "ZoneSpec",
		Label:       "Zone",
		Description: "A zone — a scope for spec overrides between network-global and per-device.",
		Sample:      ZoneSpec{},
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

	// ====================================================================
	// Read-only top-level kind — Platform support is deeply tied to the
	// backend (HWSKU, port stride, SAI compatibility); operators don't
	// author platforms via a universal UI. The schema is exposed so UIs
	// can render a read-only platform view; Create/Update/Delete paths
	// are absent.
	// ====================================================================
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "PlatformSpec",
		Label:       "Platform",
		Description: "Hardware platform definition (HWSKU, ports, VM image, feature support). Read-only via the universal UI — authoring requires deep backend coordination.",
		Sample:      PlatformSpec{},
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
	})
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "FilterRule",
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
	RegisterSchemaKind(SchemaRegistration{
		Kind:        "RoutePolicyRule",
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
		Description: "Per-device EVPN BGP overlay peering — embedded on a device profile.",
		Sample:      EVPNConfig{},
	})
}
