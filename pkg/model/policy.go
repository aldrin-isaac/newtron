package model

// RoutingPolicy represents a BGP routing policy
type RoutingPolicy struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Statements  []*PolicyStatement `json:"statements"`
}

// PolicyStatement represents a single policy statement
type PolicyStatement struct {
	Name       string        `json:"name"`
	Sequence   int           `json:"sequence"`
	Conditions *PolicyMatch  `json:"conditions,omitempty"`
	Actions    *PolicyAction `json:"actions"`
}

// PolicyMatch represents match conditions for a policy
type PolicyMatch struct {
	PrefixList    string `json:"prefix_list,omitempty"`
	PrefixListV6  string `json:"prefix_list_v6,omitempty"`
	CommunityList string `json:"community_list,omitempty"`
	ASPathList    string `json:"as_path_list,omitempty"`
	NextHop       string `json:"next_hop,omitempty"`
	LocalPref     int    `json:"local_pref,omitempty"`
	MED           int    `json:"med,omitempty"`
	Origin        string `json:"origin,omitempty"`          // igp, egp, incomplete
	RouteType     string `json:"route_type,omitempty"`      // internal, external
	EVPNRouteType int    `json:"evpn_route_type,omitempty"` // 2, 3, 5
}

// PolicyAction represents actions to take when a policy matches
type PolicyAction struct {
	Result          string   `json:"result"` // permit, deny
	SetLocalPref    int      `json:"set_local_pref,omitempty"`
	SetMED          int      `json:"set_med,omitempty"`
	SetNextHop      string   `json:"set_next_hop,omitempty"`
	SetCommunity    []string `json:"set_community,omitempty"`
	AddCommunity    []string `json:"add_community,omitempty"`
	DeleteCommunity []string `json:"delete_community,omitempty"`
	SetOrigin       string   `json:"set_origin,omitempty"`
	PrependAS       int      `json:"prepend_as,omitempty"` // AS path prepend count
	SetWeight       int      `json:"set_weight,omitempty"`
}

// PrefixList represents an IP prefix list
type PrefixList struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Family      string             `json:"family"` // ipv4, ipv6
	Entries     []*PrefixListEntry `json:"entries"`
}

// PrefixListEntry represents a single prefix list entry
type PrefixListEntry struct {
	Sequence int    `json:"sequence"`
	Action   string `json:"action"`       // permit, deny
	Prefix   string `json:"prefix"`       // CIDR notation
	GE       int    `json:"ge,omitempty"` // Minimum prefix length
	LE       int    `json:"le,omitempty"` // Maximum prefix length
}

// CommunityList represents a BGP community list
type CommunityList struct {
	Name    string                `json:"name"`
	Type    string                `json:"type"` // standard, extended, expanded
	Entries []*CommunityListEntry `json:"entries"`
}

// CommunityListEntry represents a community list entry
type CommunityListEntry struct {
	Sequence  int    `json:"sequence"`
	Action    string `json:"action"`    // permit, deny
	Community string `json:"community"` // Community value or regex
}

// ASPathList represents a BGP AS-path access list
type ASPathList struct {
	Name    string         `json:"name"`
	Entries []*ASPathEntry `json:"entries"`
}

// ASPathEntry represents an AS-path list entry
type ASPathEntry struct {
	Sequence int    `json:"sequence"`
	Action   string `json:"action"` // permit, deny
	Regex    string `json:"regex"`  // AS path regex
}

// NewRoutingPolicy creates a new routing policy
func NewRoutingPolicy(name string) *RoutingPolicy {
	return &RoutingPolicy{
		Name: name,
	}
}

// AddStatement adds a statement to the policy
func (p *RoutingPolicy) AddStatement(stmt *PolicyStatement) {
	// Insert in sequence order
	for i, s := range p.Statements {
		if stmt.Sequence < s.Sequence {
			p.Statements = append(p.Statements[:i], append([]*PolicyStatement{stmt}, p.Statements[i:]...)...)
			return
		}
	}
	p.Statements = append(p.Statements, stmt)
}

// NewPrefixList creates a new prefix list
func NewPrefixList(name, family string) *PrefixList {
	return &PrefixList{
		Name:   name,
		Family: family,
	}
}

// AddEntry adds an entry to the prefix list
func (p *PrefixList) AddEntry(entry *PrefixListEntry) {
	// Insert in sequence order
	for i, e := range p.Entries {
		if entry.Sequence < e.Sequence {
			p.Entries = append(p.Entries[:i], append([]*PrefixListEntry{entry}, p.Entries[i:]...)...)
			return
		}
	}
	p.Entries = append(p.Entries, entry)
}

// GetEntry returns an entry by sequence number
func (p *PrefixList) GetEntry(sequence int) *PrefixListEntry {
	for _, e := range p.Entries {
		if e.Sequence == sequence {
			return e
		}
	}
	return nil
}
