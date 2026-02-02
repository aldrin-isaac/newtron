package util

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ParseIPWithMask parses an IP address with CIDR notation
// Returns the IP, mask length, and any error
func ParseIPWithMask(cidr string) (net.IP, int, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid CIDR notation: %s", cidr)
	}
	ones, _ := ipNet.Mask.Size()
	return ip, ones, nil
}

// ComputeNeighborIP returns the peer IP for point-to-point subnets (/30 or /31)
// Returns empty string if not a point-to-point subnet
func ComputeNeighborIP(localIP string, maskLen int) string {
	ip := net.ParseIP(localIP)
	if ip == nil {
		return ""
	}
	ip = ip.To4()
	if ip == nil {
		return "" // IPv6 not supported for this function
	}

	switch maskLen {
	case 31: // RFC 3021 point-to-point
		// /31: two usable IPs, neighbor is the other one
		if ip[3]&1 == 0 {
			ip[3]++
		} else {
			ip[3]--
		}
	case 30: // Traditional point-to-point
		// /30: .0=network, .1=first host, .2=second host, .3=broadcast
		lastOctet := ip[3] & 0x03
		if lastOctet == 1 {
			ip[3]++
		} else if lastOctet == 2 {
			ip[3]--
		} else {
			return "" // Network or broadcast address
		}
	default:
		return "" // Not a point-to-point link
	}
	return ip.String()
}

// ComputeNetworkAddr returns the network address for a given IP and mask
func ComputeNetworkAddr(ipStr string, maskLen int) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	ip = ip.To4()
	if ip == nil {
		return ""
	}

	mask := net.CIDRMask(maskLen, 32)
	network := ip.Mask(mask)
	return network.String()
}

// ComputeBroadcastAddr returns the broadcast address for a given IP and mask
func ComputeBroadcastAddr(ipStr string, maskLen int) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	ip = ip.To4()
	if ip == nil {
		return ""
	}

	mask := net.CIDRMask(maskLen, 32)
	network := ip.Mask(mask)

	broadcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		broadcast[i] = network[i] | ^mask[i]
	}
	return broadcast.String()
}

// IsValidIPv4 checks if a string is a valid IPv4 address
func IsValidIPv4(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	return ip != nil && ip.To4() != nil
}

// IsValidIPv4CIDR checks if a string is a valid IPv4 CIDR notation
func IsValidIPv4CIDR(cidr string) bool {
	_, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	// Ensure it's IPv4
	parts := strings.Split(cidr, "/")
	ip := net.ParseIP(parts[0])
	return ip != nil && ip.To4() != nil
}

// IsValidMACAddress checks if a string is a valid MAC address
func IsValidMACAddress(mac string) bool {
	_, err := net.ParseMAC(mac)
	return err == nil
}

// NormalizeMACAddress normalizes a MAC address to lowercase with colons
func NormalizeMACAddress(mac string) (string, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return "", err
	}
	return hw.String(), nil
}

// IPInRange checks if an IP is within a given CIDR range
func IPInRange(ipStr, cidr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return ipNet.Contains(ip)
}

// ValidateVLANID checks if a VLAN ID is valid (1-4094)
func ValidateVLANID(vlanID int) error {
	if vlanID < 1 || vlanID > 4094 {
		return fmt.Errorf("VLAN ID must be between 1 and 4094, got %d", vlanID)
	}
	return nil
}

// ValidateVNI checks if a VNI is valid (1-16777215)
func ValidateVNI(vni int) error {
	if vni < 1 || vni > 16777215 {
		return fmt.Errorf("VNI must be between 1 and 16777215, got %d", vni)
	}
	return nil
}

// ValidateASN checks if an AS number is valid
func ValidateASN(asn int) error {
	if asn < 1 || asn > 4294967295 {
		return fmt.Errorf("AS number must be between 1 and 4294967295, got %d", asn)
	}
	return nil
}

// ValidateMTU checks if MTU is within valid range
func ValidateMTU(mtu int) error {
	if mtu < 68 || mtu > 9216 {
		return fmt.Errorf("MTU must be between 68 and 9216, got %d", mtu)
	}
	return nil
}

// ParsePortRange parses a port range string like "1024-65535"
func ParsePortRange(rangeStr string) (start, end int, err error) {
	parts := strings.Split(rangeStr, "-")
	if len(parts) == 1 {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port: %s", parts[0])
		}
		if port < 0 || port > 65535 {
			return 0, 0, fmt.Errorf("port out of range: %d", port)
		}
		return port, port, nil
	}
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid port range format: %s", rangeStr)
	}

	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start port: %s", parts[0])
	}
	end, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end port: %s", parts[1])
	}

	if start < 0 || start > 65535 || end < 0 || end > 65535 {
		return 0, 0, fmt.Errorf("port out of range")
	}
	if start > end {
		return 0, 0, fmt.Errorf("start port %d greater than end port %d", start, end)
	}
	return start, end, nil
}

// FormatRouteDistinguisher generates an RD from router ID and index
func FormatRouteDistinguisher(routerID string, index int) string {
	return fmt.Sprintf("%s:%d", routerID, index)
}

// FormatRouteTarget generates an RT from ASN and value
func FormatRouteTarget(asn, value int) string {
	return fmt.Sprintf("%d:%d", asn, value)
}

// SplitIPMask splits a CIDR notation into IP and mask length
// Returns the IP (without mask) and mask length
func SplitIPMask(cidr string) (string, int) {
	parts := strings.Split(cidr, "/")
	if len(parts) != 2 {
		return cidr, 0 // Return as-is if no mask
	}
	maskLen, err := strconv.Atoi(parts[1])
	if err != nil {
		return parts[0], 0
	}
	return parts[0], maskLen
}

// DeriveNeighborIP derives the BGP neighbor IP from a local IP address with CIDR mask.
// Works for point-to-point links (/30 and /31).
// Returns error if the subnet is not point-to-point.
func DeriveNeighborIP(localIPWithMask string) (string, error) {
	ipStr, maskLen := SplitIPMask(localIPWithMask)
	if maskLen == 0 {
		return "", fmt.Errorf("IP address must include CIDR mask (e.g., 10.1.1.1/30)")
	}

	neighborIP := ComputeNeighborIP(ipStr, maskLen)
	if neighborIP == "" {
		return "", fmt.Errorf("cannot derive neighbor IP: /%d is not a point-to-point subnet (use /30 or /31)", maskLen)
	}
	return neighborIP, nil
}
