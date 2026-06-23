package spec

// scope.go — the scope vocabulary for the hierarchical spec store.
//
// Specs participate in network → zone → node resolution (OverridableSpecs,
// see DESIGN_PRINCIPLES_NEWTRON §7): a kind may be defined at any of three
// scopes, with node overriding zone overriding network. These constants name
// those scopes once, so every layer that flattens or reports the hierarchy
// (the cross-scope inventory, the API wire surface) speaks the same tokens.
const (
	ScopeNetwork = "network" // top scope — defined in network.json
	ScopeZone    = "zone"    // a zone's overrides — network.json zones[<name>]
	ScopeNode    = "node"    // a device profile's overrides — nodes/<name>.json
)
