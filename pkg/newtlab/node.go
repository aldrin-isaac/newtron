package newtlab

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// NodeConfig holds the fully resolved VM configuration for a single device.
// Values are resolved from profile > platform > built-in defaults.
type NodeConfig struct {
	Name         string
	Platform     string
	DeviceType   string // "switch" (default) or "host" — from platform
	Image        string // resolved: profile > platform > error
	Memory       int    // resolved: profile > platform > 4096
	CPUs         int    // resolved: profile > platform > 2
	NICDriver    string // resolved: platform > "e1000"
	InterfaceMap string // resolved: platform > "stride-4"
	CPUFeatures  string // resolved: platform > ""
	SSHUser      string // resolved: profile ssh_user > "admin"
	SSHPass      string // resolved: profile ssh_pass > platform credentials pass
	ConsoleUser  string // resolved: platform vm_credentials user (image default user)
	ConsolePass  string // resolved: platform vm_credentials pass (image default password)
	BootTimeout  int    // resolved: platform > 180
	Host         string // from profile vm_host
	SSHPort      int    // allocated
	ConsolePort  int    // allocated
	NICs         []NICConfig
}

// NICConfig represents a single QEMU NIC attachment.
type NICConfig struct {
	Index       int    // QEMU NIC index (0=mgmt, 1..N=data)
	NetdevID    string // "mgmt", "eth1", "eth2", ...
	Interface   string // SONiC interface name ("Ethernet0", etc.) or "mgmt"
	ConnectAddr string // "IP:PORT" for data NICs (connects to bridge worker), empty for mgmt
}

// ResolveNodeConfig builds a NodeConfig for a device by merging
// profile overrides with platform defaults and built-in fallbacks.
// Returns error if no vm_image can be resolved.
func ResolveNodeConfig(
	name string,
	profile *spec.DeviceProfile,
	platform *spec.PlatformSpec,
) (*NodeConfig, error) {
	nc := &NodeConfig{
		Name:     name,
		Platform: profile.Platform,
	}

	// DeviceType: from platform (default "switch")
	if platform != nil && platform.DeviceType != "" {
		nc.DeviceType = platform.DeviceType
	}

	// Image: profile > platform > error
	switch {
	case profile.VMImage != "":
		nc.Image = profile.VMImage
	case platform != nil && platform.VMImage != "":
		nc.Image = platform.VMImage
	default:
		return nil, fmt.Errorf("newtlab: no vm_image for device %s (check platform or profile)", name)
	}

	// Memory: profile > platform > 4096
	switch {
	case profile.VMMemory > 0:
		nc.Memory = profile.VMMemory
	case platform != nil && platform.VMMemory > 0:
		nc.Memory = platform.VMMemory
	default:
		nc.Memory = 4096
	}

	// CPUs: profile > platform > 2
	switch {
	case profile.VMCPUs > 0:
		nc.CPUs = profile.VMCPUs
	case platform != nil && platform.VMCPUs > 0:
		nc.CPUs = platform.VMCPUs
	default:
		nc.CPUs = 2
	}

	// NICDriver: platform > "e1000"
	if platform != nil && platform.VMNICDriver != "" {
		nc.NICDriver = platform.VMNICDriver
	} else {
		nc.NICDriver = "e1000"
	}

	// InterfaceMap: platform > "stride-4"
	if platform != nil && platform.VMInterfaceMap != "" {
		nc.InterfaceMap = platform.VMInterfaceMap
	} else {
		nc.InterfaceMap = "stride-4"
	}

	// CPUFeatures: platform > ""
	if platform != nil {
		nc.CPUFeatures = platform.VMCPUFeatures
	}

	// ConsoleUser/ConsolePass: platform vm_credentials (the user baked into the image)
	if platform != nil && platform.VMCredentials != nil {
		nc.ConsoleUser = platform.VMCredentials.User
		nc.ConsolePass = platform.VMCredentials.Pass
	}

	// SSHUser: profile > "admin"
	if profile.SSHUser != "" {
		nc.SSHUser = profile.SSHUser
	} else {
		nc.SSHUser = "admin"
	}

	// SSHPass: profile > platform credentials pass
	switch {
	case profile.SSHPass != "":
		nc.SSHPass = profile.SSHPass
	case platform != nil && platform.VMCredentials != nil:
		nc.SSHPass = platform.VMCredentials.Pass
	}

	// BootTimeout: platform > 180
	if platform != nil && platform.VMBootTimeout > 0 {
		nc.BootTimeout = platform.VMBootTimeout
	} else {
		nc.BootTimeout = 180
	}

	// Host: profile > "" (local)
	nc.Host = profile.VMHost

	// NIC 0 is always management
	nc.NICs = []NICConfig{{
		Index:     0,
		NetdevID:  "mgmt",
		Interface: "mgmt",
	}}

	return nc, nil
}
