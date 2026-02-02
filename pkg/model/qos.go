package model

import (
	"fmt"
	"strings"
)

// QoSProfile maps interface types to scheduler configurations
type QoSProfile struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	SchedulerMap string            `json:"scheduler_map"` // e.g., "8q" or "4q"
	TrustDSCP    bool              `json:"trust_dscp"`    // Trust incoming DSCP
	DSCPToTCMap  string            `json:"dscp_to_tc_map,omitempty"`
	TCToQueueMap string            `json:"tc_to_queue_map,omitempty"`
	Policers     map[string]string `json:"policers,omitempty"` // direction -> policer name
}

// CoSClass defines a Class of Service with DSCP mappings
type CoSClass struct {
	Name     string `json:"name"`     // be, ef, cs1-7, af11-42
	DSCP     int    `json:"dscp"`     // DSCP value (0-63)
	Queue    string `json:"queue"`    // Forwarding class (be, q1-q4, ef, sc, nc)
	Priority string `json:"priority"` // low, high
	Color    string `json:"color"`    // green, yellow, red (drop priority)
}

// Scheduler defines queue bandwidth allocation
type Scheduler struct {
	Name         string `json:"name"`                    // e.g., "be-8q", "ef-8q"
	Queue        int    `json:"queue"`                   // Queue number (0-7)
	Type         string `json:"type"`                    // DWRR, STRICT
	Weight       int    `json:"weight,omitempty"`        // For DWRR
	Priority     string `json:"priority"`                // strict, low
	TransmitRate int    `json:"transmit_rate,omitempty"` // Percent of bandwidth
	DropProfile  string `json:"drop_profile,omitempty"`  // RED profile name
}

// DropProfile defines RED (Random Early Detection) curves
type DropProfile struct {
	Name            string `json:"name"`          // e.g., "red-low-tcp"
	MinThreshold    int    `json:"min_thresh"`    // Start dropping (bytes or %)
	MaxThreshold    int    `json:"max_thresh"`    // 100% drop threshold
	DropProbability int    `json:"drop_prob"`     // Max drop probability (%)
	ECN             bool   `json:"ecn,omitempty"` // Enable ECN marking
}

// Policer for rate limiting
type Policer struct {
	Name         string `json:"name"`
	CIR          int64  `json:"cir"`           // Committed Information Rate (bps)
	CBS          int64  `json:"cbs"`           // Committed Burst Size (bytes)
	PIR          int64  `json:"pir,omitempty"` // Peak Information Rate (bps)
	PBS          int64  `json:"pbs,omitempty"` // Peak Burst Size (bytes)
	MeterType    string `json:"meter_type"`    // sr_tcm, tr_tcm
	Mode         string `json:"mode"`          // color-blind, color-aware
	RedAction    string `json:"red_action"`    // drop, remark
	YellowAction string `json:"yellow_action,omitempty"`
}

// DSCPToTCMap maps DSCP values to traffic classes
type DSCPToTCMap struct {
	Name    string      `json:"name"`
	Entries map[int]int `json:"entries"` // DSCP -> TC
}

// TCToQueueMap maps traffic classes to queues
type TCToQueueMap struct {
	Name    string      `json:"name"`
	Entries map[int]int `json:"entries"` // TC -> Queue
}

// PortQoSMap represents QoS maps applied to a port
type PortQoSMap struct {
	Port         string `json:"port"`
	DSCPToTCMap  string `json:"dscp_to_tc_map,omitempty"`
	TCToQueueMap string `json:"tc_to_queue_map,omitempty"`
	Scheduler    string `json:"scheduler,omitempty"`
}

// QueueConfig represents per-queue configuration
type QueueConfig struct {
	Port        string `json:"port"`
	Queue       int    `json:"queue"`
	Scheduler   string `json:"scheduler"`
	WREDProfile string `json:"wred_profile,omitempty"`
}

// Standard DSCP values
const (
	DSCPBestEffort = 0  // BE - Best Effort
	DSCPCs1        = 8  // CS1 - Scavenger
	DSCPAf11       = 10 // AF11
	DSCPAf12       = 12 // AF12
	DSCPAf13       = 14 // AF13
	DSCPCs2        = 16 // CS2 - OAM
	DSCPAf21       = 18 // AF21
	DSCPAf22       = 20 // AF22
	DSCPAf23       = 22 // AF23
	DSCPCs3        = 24 // CS3 - Signaling
	DSCPAf31       = 26 // AF31
	DSCPAf32       = 28 // AF32
	DSCPAf33       = 30 // AF33
	DSCPCs4        = 32 // CS4
	DSCPAf41       = 34 // AF41
	DSCPAf42       = 36 // AF42
	DSCPAf43       = 38 // AF43
	DSCPCs5        = 40 // CS5
	DSCPEf         = 46 // EF - Expedited Forwarding (voice)
	DSCPCs6        = 48 // CS6 - Network Control
	DSCPCs7        = 56 // CS7 - Network Control (highest)
)

// Standard 8-Queue scheduler weights
var Standard8QueueWeights = map[int]int{
	0: 20, // BE
	1: 20, // Q1
	2: 20, // Q2
	3: 10, // Q3
	4: 10, // Q4
	5: 10, // EF (strict)
	6: 5,  // SC
	7: 5,  // NC (strict)
}

// NewQoSProfile creates a new QoS profile
func NewQoSProfile(name string, schedulerMap string) *QoSProfile {
	return &QoSProfile{
		Name:         name,
		SchedulerMap: schedulerMap,
	}
}

// NewPolicer creates a new policer
func NewPolicer(name string, cir, cbs int64) *Policer {
	return &Policer{
		Name:      name,
		CIR:       cir,
		CBS:       cbs,
		MeterType: "sr_tcm",
		Mode:      "color-blind",
		RedAction: "drop",
	}
}

// ParseBandwidth parses bandwidth strings like "100m", "1g", "10k"
// Returns the value in bits per second
func ParseBandwidth(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty bandwidth string")
	}

	s = strings.ToLower(strings.TrimSpace(s))

	// Check for suffix
	var multiplier int64 = 1
	var numStr string

	if strings.HasSuffix(s, "gbps") || strings.HasSuffix(s, "g") {
		multiplier = 1000000000
		numStr = strings.TrimSuffix(strings.TrimSuffix(s, "gbps"), "g")
	} else if strings.HasSuffix(s, "mbps") || strings.HasSuffix(s, "m") {
		multiplier = 1000000
		numStr = strings.TrimSuffix(strings.TrimSuffix(s, "mbps"), "m")
	} else if strings.HasSuffix(s, "kbps") || strings.HasSuffix(s, "k") {
		multiplier = 1000
		numStr = strings.TrimSuffix(strings.TrimSuffix(s, "kbps"), "k")
	} else if strings.HasSuffix(s, "bps") {
		multiplier = 1
		numStr = strings.TrimSuffix(s, "bps")
	} else {
		// Assume raw bps if no suffix
		numStr = s
	}

	var value float64
	if _, err := fmt.Sscanf(numStr, "%f", &value); err != nil {
		return 0, fmt.Errorf("invalid bandwidth value: %s", s)
	}

	return int64(value * float64(multiplier)), nil
}

// FormatBandwidth formats a bandwidth value in bps to human-readable form
func FormatBandwidth(bps int64) string {
	if bps >= 1000000000 {
		return fmt.Sprintf("%.1fg", float64(bps)/1000000000)
	} else if bps >= 1000000 {
		return fmt.Sprintf("%.1fm", float64(bps)/1000000)
	} else if bps >= 1000 {
		return fmt.Sprintf("%.1fk", float64(bps)/1000)
	}
	return fmt.Sprintf("%d", bps)
}

// NewScheduler creates a new scheduler
func NewScheduler(name string, queue int, schedulerType string) *Scheduler {
	return &Scheduler{
		Name:  name,
		Queue: queue,
		Type:  schedulerType,
	}
}

// NewDropProfile creates a new WRED drop profile
func NewDropProfile(name string, minThresh, maxThresh, dropProb int) *DropProfile {
	return &DropProfile{
		Name:            name,
		MinThreshold:    minThresh,
		MaxThreshold:    maxThresh,
		DropProbability: dropProb,
	}
}
