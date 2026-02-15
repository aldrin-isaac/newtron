package newtlab

import (
	"testing"

	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

func TestPlaceNodes_AllUnpinned(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1":  {Name: "leaf1"},
		"leaf2":  {Name: "leaf2"},
		"spine1": {Name: "spine1"},
		"spine2": {Name: "spine2"},
	}
	servers := []*spec.ServerConfig{
		{Name: "server-a", Address: "10.0.0.1", MaxNodes: 2},
		{Name: "server-b", Address: "10.0.0.2", MaxNodes: 2},
	}

	if err := PlaceNodes(nodes, servers); err != nil {
		t.Fatalf("PlaceNodes failed: %v", err)
	}

	countA, countB := 0, 0
	for _, n := range nodes {
		switch n.Host {
		case "server-a":
			countA++
		case "server-b":
			countB++
		default:
			t.Errorf("unexpected host %q", n.Host)
		}
	}
	if countA != 2 || countB != 2 {
		t.Errorf("expected 2/2 split, got server-a=%d server-b=%d", countA, countB)
	}
}

func TestPlaceNodes_PinnedAndUnpinned(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1":  {Name: "leaf1", Host: "server-a"}, // pinned
		"leaf2":  {Name: "leaf2"},
		"spine1": {Name: "spine1"},
		"spine2": {Name: "spine2"},
	}
	servers := []*spec.ServerConfig{
		{Name: "server-a", Address: "10.0.0.1", MaxNodes: 2},
		{Name: "server-b", Address: "10.0.0.2", MaxNodes: 2},
	}

	if err := PlaceNodes(nodes, servers); err != nil {
		t.Fatalf("PlaceNodes failed: %v", err)
	}

	if nodes["leaf1"].Host != "server-a" {
		t.Errorf("pinned node moved: got %q, want server-a", nodes["leaf1"].Host)
	}

	countA, countB := 0, 0
	for _, n := range nodes {
		switch n.Host {
		case "server-a":
			countA++
		case "server-b":
			countB++
		}
	}
	// With 1 pinned to A, spread puts 2 on B and 1 more on A → A=2, B=2
	if countA != 2 || countB != 2 {
		t.Errorf("expected 2/2 split, got server-a=%d server-b=%d", countA, countB)
	}
}

func TestPlaceNodes_OverCapacity(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1": {Name: "leaf1"},
		"leaf2": {Name: "leaf2"},
		"leaf3": {Name: "leaf3"},
	}
	servers := []*spec.ServerConfig{
		{Name: "server-a", Address: "10.0.0.1", MaxNodes: 1},
		{Name: "server-b", Address: "10.0.0.2", MaxNodes: 1},
	}

	err := PlaceNodes(nodes, servers)
	if err == nil {
		t.Fatal("expected error for over-capacity, got nil")
	}
}

func TestPlaceNodes_UnlimitedCapacity(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1":  {Name: "leaf1"},
		"leaf2":  {Name: "leaf2"},
		"spine1": {Name: "spine1"},
		"spine2": {Name: "spine2"},
		"spine3": {Name: "spine3"},
	}
	servers := []*spec.ServerConfig{
		{Name: "server-a", Address: "10.0.0.1", MaxNodes: 0}, // unlimited
	}

	if err := PlaceNodes(nodes, servers); err != nil {
		t.Fatalf("PlaceNodes failed: %v", err)
	}

	for name, n := range nodes {
		if n.Host != "server-a" {
			t.Errorf("node %s: got host %q, want server-a", name, n.Host)
		}
	}
}

func TestPlaceNodes_PinnedToUnknownServer(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1": {Name: "leaf1", Host: "nonexistent"},
	}
	servers := []*spec.ServerConfig{
		{Name: "server-a", Address: "10.0.0.1"},
	}

	err := PlaceNodes(nodes, servers)
	if err == nil {
		t.Fatal("expected error for unknown server, got nil")
	}
}

func TestPlaceNodes_Deterministic(t *testing.T) {
	makeNodes := func() map[string]*NodeConfig {
		return map[string]*NodeConfig{
			"leaf1":  {Name: "leaf1"},
			"leaf2":  {Name: "leaf2"},
			"spine1": {Name: "spine1"},
			"spine2": {Name: "spine2"},
		}
	}
	servers := []*spec.ServerConfig{
		{Name: "server-a", Address: "10.0.0.1", MaxNodes: 3},
		{Name: "server-b", Address: "10.0.0.2", MaxNodes: 3},
	}

	// Run placement multiple times and check results are identical
	nodes1 := makeNodes()
	if err := PlaceNodes(nodes1, servers); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		nodes2 := makeNodes()
		if err := PlaceNodes(nodes2, servers); err != nil {
			t.Fatal(err)
		}
		for name := range nodes1 {
			if nodes1[name].Host != nodes2[name].Host {
				t.Fatalf("run %d: %s placed on %q, first run was %q", i, name, nodes2[name].Host, nodes1[name].Host)
			}
		}
	}
}

func TestPlaceNodes_NoServers(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1": {Name: "leaf1"},
	}

	if err := PlaceNodes(nodes, nil); err != nil {
		t.Fatalf("PlaceNodes with nil servers should be no-op, got: %v", err)
	}
	if nodes["leaf1"].Host != "" {
		t.Errorf("node should remain unplaced, got host %q", nodes["leaf1"].Host)
	}
}

func TestPlaceNodes_HeterogeneousCapacity(t *testing.T) {
	nodes := map[string]*NodeConfig{
		"leaf1":  {Name: "leaf1"},
		"leaf2":  {Name: "leaf2"},
		"leaf3":  {Name: "leaf3"},
		"spine1": {Name: "spine1"},
		"spine2": {Name: "spine2"},
	}
	servers := []*spec.ServerConfig{
		{Name: "big", Address: "10.0.0.1", MaxNodes: 4},
		{Name: "small", Address: "10.0.0.2", MaxNodes: 1},
	}

	if err := PlaceNodes(nodes, servers); err != nil {
		t.Fatalf("PlaceNodes failed: %v", err)
	}

	countBig, countSmall := 0, 0
	for _, n := range nodes {
		switch n.Host {
		case "big":
			countBig++
		case "small":
			countSmall++
		}
	}
	// small can only hold 1, big gets the rest
	if countSmall != 1 {
		t.Errorf("small server: got %d nodes, want 1", countSmall)
	}
	if countBig != 4 {
		t.Errorf("big server: got %d nodes, want 4", countBig)
	}
}
