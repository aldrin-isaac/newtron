package node

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// configDBReader is the minimal read surface verifyWithReader needs.
// *sonic.ConfigDBClient satisfies it; tests inject a fake.
type configDBReader interface {
	Get(table, key string) (map[string]string, error)
	Exists(table, key string) (bool, error)
}

// formatRedisHash renders a CONFIG_DB hash as a deterministic single-line
// string suitable for VerificationError.DeviceResponse. Fields are sorted by
// name so the output is stable across map iteration order.
// Example: "asn=65001 enabled=true router_id=10.0.0.1"
func formatRedisHash(m map[string]string) string {
	if len(m) == 0 {
		return "(empty hash)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " ")
}

// ChangeType is an alias for sonic.ChangeType, re-exported for convenience.
// All new code should prefer sonic.ChangeType directly.
type ChangeType = sonic.ChangeType

// Re-export sonic.ChangeType constants so existing code compiles without changes.
const (
	ChangeAdd    = sonic.ChangeTypeAdd
	ChangeModify = sonic.ChangeTypeModify
	ChangeDelete = sonic.ChangeTypeDelete
)

// Change is an alias for sonic.ConfigChange. All external references to
// node.Change (audit/event.go, test helpers) continue to compile.
type Change = sonic.ConfigChange

// ChangeSet represents a collection of configuration changes.
type ChangeSet struct {
	Device       string                     `json:"device"`
	Operation    string                     `json:"operation"`
	Timestamp    time.Time                  `json:"timestamp"`
	Changes      []Change                   `json:"changes"`
	AppliedCount int                        `json:"applied_count"`            // number of changes successfully written by Apply(); 0 before Apply()
	Verification *sonic.VerificationResult `json:"verification,omitempty"`   // populated after apply+verify in execute mode

	// DeviceOps records the outcome of each Device I/O Operation the ChangeSet
	// performed — one entry per Redis HSET/DEL during Apply, plus one
	// verify_read entry per Change during Verify. Surfaced on WriteResult.
	// See sonic.DeviceOp and DESIGN_PRINCIPLES_NEWTRON §11, §46.
	DeviceOps []sonic.DeviceOp `json:"device_ops,omitempty"`

	// OperationParams captures parameters for the intent record.
	// Populated by the operation that creates the ChangeSet (e.g., CreateVLAN
	// sets {"vlan_id": "100"}). Used by Commit() to build the operation list.
	OperationParams map[string]string `json:"operation_params,omitempty"`

	// ReverseOp is the operation name that undoes this operation.
	// Set by forward operations at creation time (e.g., "device.create-vlan"
	// sets ReverseOp="device.delete-vlan"). Empty for terminal/reverse
	// operations that have nothing to undo.
	ReverseOp string `json:"reverse_op,omitempty"`
}

// NewChangeSet creates a new ChangeSet.
func NewChangeSet(device, operation string) *ChangeSet {
	return &ChangeSet{
		Device:    device,
		Operation: operation,
		Timestamp: time.Now(),
		Changes:   make([]Change, 0),
	}
}

// add appends a change of any type (internal use by buildChangeSet, op).
func (cs *ChangeSet) add(table, key string, changeType sonic.ChangeType, fields map[string]string) {
	cs.Changes = append(cs.Changes, Change{
		Table:  table,
		Key:    key,
		Type:   changeType,
		Fields: fields,
	})
}

// Add creates a new entry.
func (cs *ChangeSet) Add(table, key string, fields map[string]string) {
	cs.add(table, key, ChangeAdd, fields)
}

// Update modifies an existing entry.
func (cs *ChangeSet) Update(table, key string, fields map[string]string) {
	cs.add(table, key, ChangeModify, fields)
}

// Delete removes an entry.
func (cs *ChangeSet) Delete(table, key string) {
	cs.add(table, key, ChangeDelete, nil)
}

// Adds bridges config function output ([]sonic.Entry) for batch creates.
func (cs *ChangeSet) Adds(entries []sonic.Entry) {
	for _, e := range entries {
		cs.Add(e.Table, e.Key, e.Fields)
	}
}

// Updates bridges config function output ([]sonic.Entry) for batch modifies.
func (cs *ChangeSet) Updates(entries []sonic.Entry) {
	for _, e := range entries {
		cs.Update(e.Table, e.Key, e.Fields)
	}
}

// Deletes bridges config function output ([]sonic.Entry) for batch deletes.
func (cs *ChangeSet) Deletes(entries []sonic.Entry) {
	for _, e := range entries {
		cs.Delete(e.Table, e.Key)
	}
}

// Prepend inserts a change at the beginning of the ChangeSet.
// Used by writeIntent to ensure the intent entry is always first,
// regardless of when intent recording is called.
func (cs *ChangeSet) Prepend(table, key string, fields map[string]string) {
	change := Change{Table: table, Key: key, Type: ChangeAdd, Fields: fields}
	cs.Changes = append([]Change{change}, cs.Changes...)
}

// Merge appends all changes from other into cs.
func (cs *ChangeSet) Merge(other *ChangeSet) {
	cs.Changes = append(cs.Changes, other.Changes...)
}

// IsEmpty returns true if there are no changes.
func (cs *ChangeSet) IsEmpty() bool {
	return len(cs.Changes) == 0
}

// buildChangeSet wraps config function output into a ChangeSet.
// Bridges pure config functions (return []sonic.Entry) with the ChangeSet
// world used by primitives and composites.
func buildChangeSet(deviceName, operation string, config []sonic.Entry, changeType sonic.ChangeType) *ChangeSet {
	cs := NewChangeSet(deviceName, operation)
	for _, e := range config {
		cs.add(e.Table, e.Key, changeType, e.Fields)
	}
	return cs
}

// op is a generic helper for simple CRUD operations. It runs precondition
// checks, calls the entry generator, and wraps the result in a ChangeSet.
// Use this for operations whose entire body is: preconditions → generate entries → done.
// Skip it for complex operations that need custom logic between precondition and return
// (e.g., ApplyService, RemoveService, SetupVXLAN).
//
// The optional reverseOp parameter sets ChangeSet.ReverseOp — the operation name
// that undoes this one. Only forward operations set this; terminal/reverse
// operations omit it.
//
// op() always updates the projection via render so subsequent operations
// (precondition checks, idempotency guards) see the effects of prior ones.
// The projection is the single derived representation in both online and
// offline modes.
func (n *Node) op(name, resource string, changeType sonic.ChangeType,
	checks func(*PreconditionChecker), gen func() []sonic.Entry, reverseOp ...string) (*ChangeSet, error) {

	pc := n.precondition(name, resource)
	if checks != nil {
		checks(pc)
	}
	if err := pc.Result(); err != nil {
		return nil, err
	}

	entries := gen()
	cs := buildChangeSet(n.name, "device."+name, entries, changeType)

	if len(reverseOp) > 0 {
		cs.ReverseOp = reverseOp[0]
	}

	// Always update the projection. render validates entries against the
	// schema, then applies them (DeleteEntry for deletes, ApplyEntries for adds).
	if err := n.render(cs); err != nil {
		return nil, err
	}

	return cs, nil
}

// String returns a human-readable representation of the changes.
func (cs *ChangeSet) String() string {
	if cs.IsEmpty() {
		return "No changes"
	}

	var sb strings.Builder
	for _, c := range cs.Changes {
		typeStr := ""
		switch c.Type {
		case ChangeAdd:
			typeStr = "[ADD]"
		case ChangeModify:
			typeStr = "[MOD]"
		case ChangeDelete:
			typeStr = "[DEL]"
		}

		sb.WriteString(fmt.Sprintf("  %s %s|%s", typeStr, c.Table, c.Key))
		if c.Fields != nil && len(c.Fields) > 0 {
			sb.WriteString(fmt.Sprintf(" → %v", c.Fields))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// Preview returns a formatted preview of the changes.
func (cs *ChangeSet) Preview() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Operation: %s\n", cs.Operation))
	sb.WriteString(fmt.Sprintf("Device: %s\n", cs.Device))
	sb.WriteString(fmt.Sprintf("Changes to CONFIG_DB:\n%s", cs.String()))
	return sb.String()
}

// validate checks all entries in the ChangeSet against the CONFIG_DB schema.
// Called by render() — the single validation point in the pipeline.
func (cs *ChangeSet) validate() error {
	return sonic.ValidateChanges(cs.Changes)
}

// Apply writes the changes to the device's config_db via Redis.
//
// Each change becomes one Device I/O Operation (HSET or DEL) and one
// corresponding DeviceOp record on cs.DeviceOps — substrate-grade
// per-operation outcome captured at the moment of execution (§46). On
// failure, the failing op is recorded with result="rejected" and the
// verbatim error in DeviceResponse, then Apply returns the wrapped error
// without attempting subsequent writes.
func (cs *ChangeSet) Apply(n *Node) error {
	// Transport guard — entries were already rendered into the projection
	// by render(cs) in op(). Without transport, skip Redis delivery.
	if n.conn == nil {
		return nil
	}

	if err := n.precondition("apply-changeset", cs.Operation).Result(); err != nil {
		return err
	}

	// No re-validation here — entries were validated at render() time before
	// entering the projection. Architecture §5: "cs.Apply(n) does NOT
	// re-validate — entries were validated when they entered the projection."

	client := n.ConfigDBClient()
	if client == nil {
		return fmt.Errorf("CONFIG_DB client not connected")
	}

	seq := len(cs.DeviceOps)
	for _, change := range cs.Changes {
		var err error
		var kind string
		var reply int64
		switch change.Type {
		case sonic.ChangeTypeAdd, sonic.ChangeTypeModify:
			kind = sonic.DeviceOpsKindRedisWrite
			reply, err = client.SetWithReply(change.Table, change.Key, change.Fields)
		case sonic.ChangeTypeDelete:
			kind = sonic.DeviceOpsKindRedisDelete
			reply, err = client.DeleteWithReply(change.Table, change.Key)
		}
		op := sonic.DeviceOp{
			Seq:    seq,
			Kind:   kind,
			Table:  change.Table,
			Key:    change.Key,
			Fields: change.Fields,
			At:     time.Now().UTC(),
		}
		if err != nil {
			op.Result = sonic.DeviceOpsResultRejected
			op.DeviceResponse = err.Error()
			cs.DeviceOps = append(cs.DeviceOps, op)
			return fmt.Errorf("applying change to %s|%s: %w", change.Table, change.Key, err)
		}
		op.Result = sonic.DeviceOpsResultApplied
		op.DeviceResponse = fmt.Sprintf("(integer) %d", reply)
		cs.DeviceOps = append(cs.DeviceOps, op)
		seq++
	}

	cs.AppliedCount = len(cs.Changes)
	return nil
}

// Verify re-reads CONFIG_DB via a fresh connection and compares against the
// ChangeSet to confirm that writes were persisted. Stores the result in
// cs.Verification and appends one verify_read DeviceOp per change to
// cs.DeviceOps — substrate-grade observability over the verify pass.
func (cs *ChangeSet) Verify(n *Node) error {
	// Transport guard — without a device connection, there is nothing to
	// verify against Redis. Same pattern as Apply (architecture §8).
	if n.conn == nil {
		return nil
	}

	result, ops, err := n.verifyConfigChanges(cs.Changes, len(cs.DeviceOps))
	if err != nil {
		return err
	}
	cs.Verification = result
	cs.DeviceOps = append(cs.DeviceOps, ops...)
	return nil
}

// verifyConfigChanges re-reads CONFIG_DB via a fresh connection and compares
// against the given changes. Used by ChangeSet.Verify.
//
// When a key appears in multiple changes (e.g., RefreshService does delete+add
// on the same key), only the final operation per key is verified. This replaces
// the former DeduplicateRefresh band-aid with correct final-state semantics.
//
// Returns the typed VerificationResult plus a []DeviceOp recording one
// verify_read entry per change (substrate observability over the verify pass).
// seqStart is the starting seq for the emitted verify_read ops so the caller
// can continue the cs.DeviceOps sequence from where Apply left off.
func (n *Node) verifyConfigChanges(changes []sonic.ConfigChange, seqStart int) (*sonic.VerificationResult, []sonic.DeviceOp, error) {
	if n.conn == nil {
		return nil, nil, util.ErrNotConnected
	}

	addr := n.conn.ConnAddr()

	freshClient := sonic.NewConfigDBClient(addr)
	if err := freshClient.Connect(); err != nil {
		return nil, nil, fmt.Errorf("fresh config_db connection: %w", err)
	}
	defer freshClient.Close()

	return verifyWithReader(freshClient, changes, seqStart)
}

// verifyWithReader holds the verification logic against any configDBReader.
// Separated from verifyConfigChanges so tests can inject a fake reader without
// a live device connection.
//
// Returns the typed VerificationResult plus a []DeviceOp containing one
// verify_read entry per change. seqStart sets the starting Seq for the
// returned ops so callers can continue an existing DeviceOp sequence.
func verifyWithReader(reader configDBReader, changes []sonic.ConfigChange, seqStart int) (*sonic.VerificationResult, []sonic.DeviceOp, error) {
	// Build final state per key: last operation wins. This correctly handles
	// merged ChangeSets where a key is deleted then re-added (RefreshService).
	type finalOp struct {
		change sonic.ConfigChange
		index  int // preserve order for deterministic iteration
	}
	final := make(map[string]*finalOp)
	for idx, change := range changes {
		key := change.Table + "|" + change.Key
		final[key] = &finalOp{change: change, index: idx}
	}

	// Collect final ops sorted by original order
	sorted := make([]sonic.ConfigChange, 0, len(final))
	for _, op := range final {
		sorted = append(sorted, op.change)
	}
	// Sort by index for deterministic verification order
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			ki := sorted[i].Table + "|" + sorted[i].Key
			kj := sorted[j].Table + "|" + sorted[j].Key
			if final[ki].index > final[kj].index {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	result := &sonic.VerificationResult{}
	ops := make([]sonic.DeviceOp, 0, len(sorted))
	seq := seqStart

	// emitVerifyRead records one verify_read DeviceOp per change.
	// Result is "applied" when re-read matched the ChangeSet, "rejected"
	// when any expected field/state did not match.
	emitVerifyRead := func(change sonic.ConfigChange, applied bool, deviceResponse string) {
		op := sonic.DeviceOp{
			Seq:            seq,
			Kind:           sonic.DeviceOpsKindVerifyRead,
			Table:          change.Table,
			Key:            change.Key,
			Result:         sonic.DeviceOpsResultApplied,
			DeviceResponse: deviceResponse,
			At:             time.Now().UTC(),
		}
		if !applied {
			op.Result = sonic.DeviceOpsResultRejected
		}
		// For verify_read of an add/modify, the substrate "Fields" we expected
		// to see is the change's Fields. For a delete, Fields stays nil.
		if change.Type != sonic.ChangeTypeDelete {
			op.Fields = change.Fields
		}
		ops = append(ops, op)
		seq++
	}

	for _, change := range sorted {
		switch change.Type {
		case sonic.ChangeTypeAdd, sonic.ChangeTypeModify:
			actual, err := reader.Get(change.Table, change.Key)
			if err != nil {
				return nil, nil, fmt.Errorf("reading %s|%s: %w", change.Table, change.Key, err)
			}
			if len(actual) == 0 {
				// Site 1: key entirely absent — HGETALL returned no fields.
				result.Failed++
				result.Errors = append(result.Errors, sonic.VerificationError{
					Table:          change.Table,
					Key:            change.Key,
					Field:          "(all)",
					Expected:       "present",
					Actual:         "",
					DeviceResponse: "(key absent — HGETALL returned no fields)",
				})
				emitVerifyRead(change, false, "(key absent — HGETALL returned no fields)")
				continue
			}
			allMatch := true
			for field, expected := range change.Fields {
				if got, ok := actual[field]; !ok || got != expected {
					result.Failed++
					allMatch = false
					actualVal := ""
					if ok {
						actualVal = got
					}
					// Site 2: field mismatch — carry the full HGETALL content so
					// the operator sees the complete key state at verify time,
					// atomic with the failure detection. Highest substrate value.
					result.Errors = append(result.Errors, sonic.VerificationError{
						Table:          change.Table,
						Key:            change.Key,
						Field:          field,
						Expected:       expected,
						Actual:         actualVal,
						DeviceResponse: formatRedisHash(actual),
					})
				}
			}
			if allMatch {
				result.Passed++
			}
			// One verify_read op per change — DeviceResponse carries the full
			// HGETALL content in both pass and fail cases (operator sees what
			// the device actually had at verify time).
			emitVerifyRead(change, allMatch, formatRedisHash(actual))
		case sonic.ChangeTypeDelete:
			exists, err := reader.Exists(change.Table, change.Key)
			if err != nil {
				return nil, nil, fmt.Errorf("checking %s|%s: %w", change.Table, change.Key, err)
			}
			if exists {
				// Site 3: key still present after delete. Fetch hash for the
				// verbatim device response; sentinel if the round-trip errors.
				deviceResp := "(key present — EXISTS returned 1)"
				if hash, err := reader.Get(change.Table, change.Key); err == nil && len(hash) > 0 {
					deviceResp = formatRedisHash(hash)
				}
				result.Failed++
				result.Errors = append(result.Errors, sonic.VerificationError{
					Table:          change.Table,
					Key:            change.Key,
					Field:          "(all)",
					Expected:       "deleted",
					Actual:         "present",
					DeviceResponse: deviceResp,
				})
				emitVerifyRead(change, false, deviceResp)
			} else {
				result.Passed++
				emitVerifyRead(change, true, "(key absent — EXISTS returned 0)")
			}
		}
	}

	return result, ops, nil
}

