package node

import (
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// seedEntry pre-populates a key in the node's projection so a subsequent
// render() has a prior ("from") state to capture.
func seedEntry(n *Node, table, key string, fields map[string]string) {
	n.configDB.ApplyEntries([]sonic.Entry{{Table: table, Key: key, Fields: fields}})
}

// TestRender_CapturesFromState pins the #236 contract: render() records the
// prior field values each change overwrites or deletes, leaves `from` empty for
// an add against an absent key, and is skipped during reconstruction.
func TestRender_CapturesFromState(t *testing.T) {
	t.Run("modify records prior fields as from", func(t *testing.T) {
		n := newTestAbstractNode()
		seedEntry(n, "VLAN", "Vlan100", map[string]string{"vlanid": "100", "description": "old"})

		cs := NewChangeSet("test-leaf", "test.modify")
		cs.add("VLAN", "Vlan100", sonic.ChangeTypeModify, map[string]string{"description": "new"})
		if err := n.render(cs); err != nil {
			t.Fatalf("render: %v", err)
		}
		c := cs.Changes[0]
		if c.From["description"] != "old" {
			t.Errorf("from[description] = %q; want the prior value 'old'", c.From["description"])
		}
		if c.Fields["description"] != "new" {
			t.Errorf("fields[description] = %q; want the new value 'new' (the `to`)", c.Fields["description"])
		}
	})

	t.Run("add against an absent key has empty from", func(t *testing.T) {
		n := newTestAbstractNode()
		cs := NewChangeSet("test-leaf", "test.add")
		cs.add("VLAN", "Vlan200", sonic.ChangeTypeAdd, map[string]string{"vlanid": "200"})
		if err := n.render(cs); err != nil {
			t.Fatalf("render: %v", err)
		}
		if cs.Changes[0].From != nil {
			t.Errorf("from = %v; want nil for an add against an absent key", cs.Changes[0].From)
		}
	})

	t.Run("delete records the deleted fields as from", func(t *testing.T) {
		n := newTestAbstractNode()
		seedEntry(n, "VRF", "Vrf_X", map[string]string{"fallback": "true"})

		cs := NewChangeSet("test-leaf", "test.delete")
		cs.add("VRF", "Vrf_X", sonic.ChangeTypeDelete, nil)
		if err := n.render(cs); err != nil {
			t.Fatalf("render: %v", err)
		}
		if cs.Changes[0].From["fallback"] != "true" {
			t.Errorf("from = %v; want the deleted fields {fallback:true}", cs.Changes[0].From)
		}
	})

	t.Run("reconstruction suppresses capture", func(t *testing.T) {
		n := newTestAbstractNode()
		seedEntry(n, "VLAN", "Vlan100", map[string]string{"description": "old"})
		n.reconstructing = true

		cs := NewChangeSet("test-leaf", "test.modify")
		cs.add("VLAN", "Vlan100", sonic.ChangeTypeModify, map[string]string{"description": "new"})
		if err := n.render(cs); err != nil {
			t.Fatalf("render: %v", err)
		}
		if cs.Changes[0].From != nil {
			t.Errorf("from = %v; want nil during reconstruction (replay must not pay for capture)", cs.Changes[0].From)
		}
	})

	// A delete-then-re-add of the same key within one ChangeSet (RefreshService
	// shape) must record each step's TRUE prior state, not the original twice:
	// the delete sees the original, the re-add sees an absent key.
	t.Run("same-key delete then re-add records each step's prior", func(t *testing.T) {
		n := newTestAbstractNode()
		seedEntry(n, "VLAN", "Vlan100", map[string]string{"description": "original"})

		cs := NewChangeSet("test-leaf", "test.refresh")
		cs.add("VLAN", "Vlan100", sonic.ChangeTypeDelete, nil)
		cs.add("VLAN", "Vlan100", sonic.ChangeTypeAdd, map[string]string{"description": "reborn"})
		if err := n.render(cs); err != nil {
			t.Fatalf("render: %v", err)
		}
		del, add := cs.Changes[0], cs.Changes[1]
		if del.From["description"] != "original" {
			t.Errorf("delete from = %v; want the original {description:original}", del.From)
		}
		if add.From != nil {
			t.Errorf("re-add from = %v; want nil (key was absent after the delete)", add.From)
		}
	})
}
