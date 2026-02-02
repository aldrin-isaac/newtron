// Package labgen generates containerlab topologies and config artifacts
// from newtron-native topology YAML definitions.
package labgen

// Topology is the top-level structure for a newtron lab topology file.
type Topology struct {
	Name     string             `yaml:"name"`
	Defaults TopologyDefaults   `yaml:"defaults"`
	Network  TopologyNetwork    `yaml:"network"`
	Nodes    map[string]NodeDef `yaml:"nodes"`
	Links    []LinkDef          `yaml:"links"`
}

// TopologyDefaults contains default values applied to all nodes.
type TopologyDefaults struct {
	Image    string `yaml:"image"`
	Kind     string `yaml:"kind,omitempty"` // containerlab kind: "sonic-vs" or "sonic-vm" (auto-detected from image if empty)
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
	Platform string `yaml:"platform"`
	Site     string `yaml:"site"`
	HWSKU    string `yaml:"hwsku"`
}

// TopologyNetwork contains network-wide settings.
type TopologyNetwork struct {
	ASNumber       int    `yaml:"as_number"`
	UnderlayASBase int    `yaml:"underlay_as_base"`
	Region         string `yaml:"region"`
}

// NodeDef defines a single node in the topology.
type NodeDef struct {
	Role       string `yaml:"role"`
	LoopbackIP string `yaml:"loopback_ip,omitempty"`
	Image      string `yaml:"image,omitempty"`
	Platform   string `yaml:"platform,omitempty"`
	Cmd        string `yaml:"cmd,omitempty"`
}

// LinkDef defines a link between two node interfaces.
type LinkDef struct {
	Endpoints []string `yaml:"endpoints"`
}
