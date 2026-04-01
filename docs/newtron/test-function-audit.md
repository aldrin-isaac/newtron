# Test Function Audit — Unified Pipeline Architecture Compliance

Systematic audit of all test programs in the newtron-server codebase against
the unified pipeline architecture (§1-§12 of
`docs/newtron/unified-pipeline-architecture.md`).

**Audit criteria:**
1. Does the test verify architectural compliance, or does it just make
   the implemented function pass?
2. Does the test exercise the intent-first model (intent DB is primary,
   projection is derived)?
3. Are there dead tests (testing deleted functions, unreachable code)?
4. Are there missing tests (architectural properties not verified)?

**Architecture summary for audit purposes:**
- Intent DB (`configDB.NewtronIntent`) is primary state
- Projection (typed CONFIG_DB tables) is derived by replaying intents via `render(cs)`
- All operational decisions read the intent DB, not the projection
- One pipeline: Intent → Replay → Render → [Deliver]
- Three states (topology offline, topology online, actuated online) differ only in intent source
- Six operations on expected state: Tree, Drift, Reconcile, Save, Reload, Clear
- `RebuildProjection` in `execute()` ensures fresh state before every operation
- Schema validation runs at render time
- Config method contract: by return, intent DB and projection are both updated

---

## File-by-File Categorization

### 1. `pkg/newtron/api/api_test.go` — API Surface Guard

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestAPICompleteness` | **Arch: API completeness** | **PASS** — Ensures every exported method on Network/Node/Interface is either covered by an HTTP endpoint or explicitly excluded with a reason. Detects stale entries in both directions. Guards against silent API drift. |

**Verdict:** 1 test, 0 issues. This is a structural compliance test — it enforces
the public API boundary design principle (CLAUDE.md).

---

### 2. `pkg/newtron/audit/audit_test.go` — Audit Logging Infrastructure

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestFileLogger_Basic` | Infrastructure | N/A — tests file-based audit logger |
| `TestFileLogger_QueryFilters` | Infrastructure | N/A |
| `TestFileLogger_QueryTimeFilter` | Infrastructure | N/A |
| `TestFileLogger_NonExistentFile` | Infrastructure | N/A |
| `TestFileLogger_QueryNonExistent` | Infrastructure | N/A |
| `TestDefaultLogger` | Infrastructure | N/A |
| `TestFileLogger_LogRotation` | Infrastructure | N/A |
| `TestFileLogger_RotationWithCleanup` | Infrastructure | N/A |
| `TestFileLogger_NewFileLoggerMkdirError` | Infrastructure | N/A |
| `TestFileLogger_NewFileLoggerOpenError` | Infrastructure | N/A |
| `TestFileLogger_QueryMalformedJSON` | Infrastructure | N/A |
| `TestFileLogger_QueryInterfaceFilter` | Infrastructure | N/A |
| `TestFileLogger_QueryEndTimeFilter` | Infrastructure | N/A |
| `TestFileLogger_QueryOffsetBeyondEvents` | Infrastructure | N/A |
| `TestFileLogger_CloseNilFile` | Infrastructure | N/A |
| `TestFileLogger_QueryReadError` | Infrastructure | N/A |

**Verdict:** 16 tests, 0 issues. Pure infrastructure — no architecture intersection.

---

### 3. `pkg/newtron/spec/feature_deps_test.go` — Feature Dependency Graph

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestFeatureDependencies` | Spec: feature deps | N/A — tests platform feature dependency resolution |
| `TestGetUnsupportedDueTo` | Spec: feature deps | N/A |
| `TestGetFeatureDependencies` | Spec: feature deps | N/A |
| `TestFeatureIndependence` | Spec: feature deps | N/A |

**Verdict:** 4 tests, 0 issues. Spec-layer infrastructure.

---

### 4. `pkg/newtron/spec/loader_test.go` — Spec Loading & Validation

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestLoader_Load` | Spec: loading | N/A |
| `TestLoader_LoadProfile` | Spec: loading | N/A |
| `TestLoader_LoadProfile_Caching` | Spec: loading | N/A |
| `TestLoader_LoadProfile_NotFound` | Spec: error paths | N/A |
| `TestLoader_DefaultSpecDir` | Spec: defaults | N/A |
| `TestLoader_ValidationErrors` | Spec: validation | N/A |
| `TestLoader_LoadMissingNetworkSpec` | Spec: error paths | N/A |
| `TestLoader_LoadMissingPlatformSpec` | Spec: error paths | N/A |
| `TestLoader_LoadInvalidJSON` | Spec: error paths | N/A |
| `TestLoader_LoadProfile_InvalidJSON` | Spec: error paths | N/A |
| `TestLoader_ValidateAllServiceErrors` | Spec: validation | N/A |
| `TestLoader_ValidateFilterRuleReferences` | Spec: validation | N/A |
| `TestLoader_ValidateProfileZoneReference` | Spec: validation | N/A |
| `TestLoader_ValidateProfile_InvalidIPs` | Spec: validation | N/A |
| `TestLoader_ValidateQoSPolicies` | Spec: validation | N/A |
| `TestLoader_ZoneLevelServiceRefsNetworkFilter` | Spec: cross-refs | N/A |
| `TestLoader_ZoneLevelServiceRefsMissing` | Spec: cross-refs | N/A |
| `TestLoader_ZoneLevelFilterRefsPrefixList` | Spec: cross-refs | N/A |
| `TestLoader_ZoneLevelServiceRefsZoneIPVPN` | Spec: cross-refs | N/A |

**Verdict:** 19 tests, 0 issues. Spec-layer infrastructure.

---

### 5. `pkg/newtron/auth/auth_test.go` — Authorization

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestContext_Chaining` | Auth: context | N/A |
| `TestChecker_SuperUser` | Auth: permissions | N/A |
| `TestChecker_GlobalPermissions` | Auth: permissions | N/A |
| `TestChecker_ServicePermissions` | Auth: permissions | N/A |
| `TestChecker_PermissionError` | Auth: permissions | N/A |
| `TestChecker_DirectUserPermission` | Auth: permissions | N/A |
| `TestChecker_CurrentUser` | Auth: permissions | N/A |
| `TestChecker_ServiceWithNilPermissions` | Auth: permissions | N/A |
| `TestChecker_GlobalPermissionNotFound` | Auth: permissions | N/A |
| `TestChecker_GlobalAllPermissionNotGranted` | Auth: permissions | N/A |
| `TestChecker_ServiceAllPermissionNotGranted` | Auth: permissions | N/A |
| `TestPermissionError_ContextVariations` | Auth: permissions | N/A |

**Verdict:** 12 tests, 0 issues. Pure infrastructure.

---

### 6. `pkg/newtron/settings/settings_test.go` — Settings Persistence

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestSettings_Defaults` | Settings | N/A |
| `TestSettings_FieldAssignment` | Settings | N/A |
| `TestSettings_SaveLoad` | Settings | N/A |
| `TestSettings_LoadNonExistent` | Settings | N/A |
| `TestSettings_LoadInvalidJSON` | Settings | N/A |
| `TestSettings_SaveCreatesDirectory` | Settings | N/A |
| `TestLoad` | Settings | N/A |
| `TestSave` | Settings | N/A |
| `TestDefaultSettingsPath` | Settings | N/A |
| `TestDefaultSettingsPath_NoHome` | Settings | N/A |
| `TestLoadFrom_ReadError` | Settings | N/A |
| `TestSaveTo_MkdirError` | Settings | N/A |

**Verdict:** 12 tests, 0 issues. Pure infrastructure.

---

### 7. `pkg/newtron/device/sonic/schema_test.go` — Schema Validation (Render Pipeline Guard)

Architecture §5: "render validates entries against the schema before any entry
enters the projection." These tests verify the validation layer that guards the
render path.

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestCheck_FieldInt` | Schema: field types | **PASS** — tests int range validation |
| `TestCheck_FieldInt_NoRange` | Schema: field types | **PASS** |
| `TestCheck_FieldEnum` | Schema: field types | **PASS** |
| `TestCheck_FieldBool` | Schema: field types | **PASS** |
| `TestCheck_FieldIP` | Schema: field types | **PASS** |
| `TestCheck_FieldCIDR` | Schema: field types | **PASS** |
| `TestCheck_FieldMAC` | Schema: field types | **PASS** |
| `TestCheck_FieldString` | Schema: field types | **PASS** |
| `TestCheck_FieldString_WithPattern` | Schema: field types | **PASS** |
| `TestValidateEntry_VLAN_ValidKey` | Schema: table validation | **PASS** |
| `TestValidateEntry_VLAN_InvalidID` | Schema: table validation | **PASS** |
| `TestValidateEntry_VLAN_InvalidKey` | Schema: table validation | **PASS** |
| `TestValidateEntry_VLAN_Vlan1_Invalid` | Schema: table validation | **PASS** |
| `TestValidateEntry_VLAN_Vlan4094_Valid` | Schema: table validation | **PASS** |
| `TestValidateEntry_VLAN_UnknownField` | Schema: fail-closed | **PASS** — verifies fail-closed behavior |
| `TestValidateEntry_BGP_NEIGHBOR_Valid` | Schema: table validation | **PASS** |
| `TestValidateEntry_BGP_NEIGHBOR_InvalidASN` | Schema: table validation | **PASS** |
| `TestValidateEntry_BGP_NEIGHBOR_InvalidIP` | Schema: table validation | **PASS** |
| `TestValidateEntry_ACL_RULE_Valid` | Schema: table validation | **PASS** |
| `TestValidateEntry_ACL_RULE_InvalidPriority` | Schema: table validation | **PASS** |
| `TestValidateEntry_INTERFACE_EmptyFields` | Schema: key-only entries | **PASS** |
| `TestValidateEntry_INTERFACE_IPSubEntry` | Schema: sub-entries | **PASS** |
| `TestValidateEntry_SAG_GLOBAL` | Schema: table validation | **PASS** |
| `TestValidateChanges_ValidBatch` | Schema: batch validation | **PASS** |
| `TestValidateChanges_UnknownTable` | Schema: fail-closed | **PASS** |
| `TestValidateChanges_DeleteSkipsFieldValidation` | Schema: delete semantics | **PASS** |
| `TestValidateChanges_DeleteValidatesKey` | Schema: delete semantics | **PASS** |
| `TestValidateChanges_MultipleErrors` | Schema: error aggregation | **PASS** |
| `TestValidateChanges_DeleteUnknownTablePasses` | Schema: delete semantics | **PASS** |
| `TestValidateChanges_AllowExtraFields` | Schema: NEWTRON_INTENT | **PASS** — AllowExtra for intent records |
| `TestValidateEntry_NEWTRON_INTENT_Valid` | Schema: intent table | **PASS** |
| `TestValidateEntry_NEWTRON_INTENT_ExtraParamsAllowed` | Schema: intent table | **PASS** |
| `TestValidateEntry_NEWTRON_INTENT_InvalidOperation` | Schema: intent table | **PASS** |
| `TestValidateEntry_NEWTRON_INTENT_InvalidState` | Schema: intent table | **PASS** |
| `TestValidateEntry_NEWTRON_HISTORY_Valid` | Schema: history table | **PASS** |
| `TestValidateEntry_NEWTRON_HISTORY_InvalidKey` | Schema: history table | **PASS** |
| `TestValidateEntry_NEWTRON_HISTORY_UnknownField` | Schema: history table | **PASS** |
| `TestValidateEntry_NEWTRON_SETTINGS_Valid` | Schema: settings table | **PASS** |
| `TestValidateEntry_NEWTRON_SETTINGS_InvalidKey` | Schema: settings table | **PASS** |
| `TestValidateEntry_NEWTRON_SETTINGS_OutOfRange` | Schema: settings table | **PASS** |
| `TestSchema_AllTablesHaveFields` | Schema: completeness | **PASS** |
| `TestSchema_KnownTables` | Schema: completeness | **PASS** |
| `TestSchema_VLANKeyPattern_BoundaryValues` | Schema: key patterns | **PASS** |
| `TestSchema_BGP_NEIGHBOR_AF_KeyPattern` | Schema: key patterns | **PASS** |

**Verdict:** 44 tests, 0 issues. Validates the render pipeline guard (§5).

---

### 8. `pkg/newtron/device/sonic/configdb_diff_test.go` — Drift Detection

Architecture §6: "Drift: Compare projection vs device → drift entries."

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestDiffConfigDB_NoDifferences` | Drift: baseline | **PASS** |
| `TestDiffConfigDB_MissingEntry` | Drift: expected missing from actual | **PASS** |
| `TestDiffConfigDB_ExtraEntry` | Drift: actual has extra | **PASS** |
| `TestDiffConfigDB_ModifiedEntry` | Drift: field mismatch | **PASS** |
| `TestDiffConfigDB_SkipsUnownedTables` | Drift: ownership filter | **PASS** |
| `TestDiffConfigDB_SkipsExcludedTables` | Drift: exclusion filter | **PASS** |
| `TestDiffConfigDB_MultipleDifferences` | Drift: aggregation | **PASS** |
| `TestFieldsMatch_SubsetWithExtra` | Drift: field comparison | **PASS** |
| `TestFieldsMatch_SameContent` | Drift: field comparison | **PASS** |
| `TestFieldsMatch_ValueDiffers` | Drift: field comparison | **PASS** |
| `TestFieldsMatch_MissingInActual` | Drift: field comparison | **PASS** |
| `TestOwnedTables_ContainsCriticalTables` | Drift: table ownership | **PASS** |
| `TestOwnedTables_ExcludesInternalTables` | Drift: table ownership | **PASS** |
| `TestExportRaw_RoundTrip` | Export: projection → raw | **PASS** |

**Verdict:** 14 tests, 0 issues. Validates the Drift operation (§6).

---

### 9. `pkg/newtron/device/sonic/configdb_parsers_test.go` — Hydration/Export Round-Trip

Architecture §9: Three mechanisms bridge data representations
(wire → struct, struct → wire, wire → pass/fail).

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestHydrators_AllTablesRegistered` | Parsers: completeness | **PASS** |
| `TestParseEntry_RoundTrip` | Parsers: hydrate→export | **PASS** — tests every typed table |
| `TestParseEntry_UnknownTable` | Parsers: error paths | **PASS** |
| `TestConfigDB_Has_Positive` | ConfigDB: accessors | **PASS** |
| `TestConfigDB_Has_Negative` | ConfigDB: accessors | **PASS** |
| `TestConfigDB_Has_NilReceiver` | ConfigDB: safety | **PASS** |
| `TestConfigDB_BGPConfigured` | ConfigDB: accessors | **PASS** |
| `TestNewEmptyConfigDB` | ConfigDB: initialization | **PASS** |
| `TestExportEntries_RoundTrip` | Export: struct→entries→struct | **PASS** — tests full projection export cycle |
| `TestStructToFields` | Export: struct→wire | **PASS** |

**Verdict:** 10 tests, 0 issues. Validates data representation mechanisms (§9).

---

### 10. `pkg/newtron/device/sonic/configdb_consistency_test.go` — Projection Consistency

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestHydrateExportRoundTrip_AllTypedTables` | **Arch: projection consistency** | **PASS** — verifies hydrate→export round-trip for ALL typed tables |
| `TestConfigTableHydrators_CoversAllTypedTables` | **Arch: completeness** | **PASS** — every typed field has a hydrator |
| `TestExportEntries_CoversAllTypedTables` | **Arch: completeness** | **PASS** — every typed field has an exporter |
| `TestDeleteEntry_CoversAllHydratedTables` | **Arch: completeness** | **PASS** — every hydrated table supports deletion |

**Verdict:** 4 tests, 0 issues. Structural compliance tests — guard projection integrity.

---

### 11. `pkg/newtron/device/sonic/statedb_parsers_test.go` — StateDB Parsing

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestNewEmptyStateDB` | StateDB: initialization | N/A |
| `TestStateParsers_AllTablesRegistered` | StateDB: completeness | N/A |
| `TestStateDB_ParseEntry` | StateDB: parsing | N/A |
| `TestStateDB_ParseEntry_UnknownTable` | StateDB: error paths | N/A |

**Verdict:** 4 tests, 0 issues. Observation primitives.

---

### 12. `pkg/newtron/device/sonic/intent_test.go` — Intent Record Structure

Architecture §1: Intent DB is primary state.

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestNewIntent` | Intent: construction | **PASS** — verifies intent record fields |
| `TestIntentToFields` | Intent: serialization | **PASS** — verifies wire format |
| `TestIntentRoundTrip` | Intent: round-trip | **PASS** — fields→intent→fields lossless |
| `TestNewIntentDefaultState` | Intent: defaults | **PASS** |
| `TestIntentStateHelpers` | Intent: state accessors | **PASS** |

**Verdict:** 5 tests, 0 issues. Validates the intent record as primary state (§1).

---

### 13. `pkg/newtron/device/sonic/device_test.go` — ConfigDB Structure & Types

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestConfigDB_JSONSerialization` | ConfigDB: serialization | **PASS** |
| `TestPortEntry_Structure` | Types: PORT | N/A |
| `TestVLANEntry_Structure` | Types: VLAN | N/A |
| `TestVLANMemberEntry_Structure` | Types: VLAN_MEMBER | N/A |
| `TestInterfaceEntry_Structure` | Types: INTERFACE | N/A |
| `TestPortChannelEntry_Structure` | Types: PORTCHANNEL | N/A |
| `TestVRFEntry_Structure` | Types: VRF | N/A |
| `TestVXLANTunnelEntry_Structure` | Types: VXLAN_TUNNEL | N/A |
| `TestVXLANMapEntry_Structure` | Types: VXLAN_TUNNEL_MAP | N/A |
| `TestEVPNNVOEntry_Structure` | Types: VXLAN_EVPN_NVO | N/A |
| `TestBGPGlobalsEntry_Structure` | Types: BGP_GLOBALS | N/A |
| `TestBGPNeighborEntry_Structure` | Types: BGP_NEIGHBOR | N/A |
| `TestACLTableEntry_JSON` | Types: ACL_TABLE | N/A |
| `TestRequireFrrcfgd_Unified` | Config mode: frrcfgd | N/A |
| `TestRequireFrrcfgd_NotSet` | Config mode: frrcfgd | N/A |
| `TestRequireFrrcfgd_Split` | Config mode: frrcfgd | N/A |
| `TestFrrcfgdMetadataFields` | Config mode: frrcfgd | N/A |
| `TestIsUnifiedConfigMode` | Config mode: detection | N/A |
| `TestConfigDB_EmptyInit` | ConfigDB: initialization | **PASS** |
| `TestProjection_CoversAllSchemaTables` | **Arch: projection completeness** | **PASS** — verifies every schema table has a projection field |
| `TestProjection_HydrateDeleteRoundTrip` | **Arch: projection integrity** | **PASS** — hydrate then delete restores empty |
| `TestConfigDB_ComplexJSON` | ConfigDB: complex cases | **PASS** |

**Verdict:** 22 tests, 0 issues. Two structural compliance tests
(`TestProjection_CoversAllSchemaTables`, `TestProjection_HydrateDeleteRoundTrip`).

---

### 14. `pkg/newtron/network/node/intent_test.go` — Intent System Core

Architecture §1 (Intent DB is primary), §4 (config methods),
§10 (intent round-trip completeness).

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestNodeIntentAccessors` | Intent: CRUD | **PASS** — reads/writes intent DB directly |
| `TestNodeLoadIntentsFromConfigDB` | Intent: reconstruction | **PASS** — tests loading intents from raw configDB |
| `TestSnapshot` | Intent: Tree (§6) | **PASS** — reads intent DB, builds DAG |
| `TestSnapshotRoundTrip` | **Arch: round-trip completeness** | **PASS** — snapshot→replay produces same intents |
| `TestWriteIntentRecordsToProjection` | **Arch: render path** | **PASS** — writeIntent updates projection via renderIntent |
| `TestWriteIntentPrepends` | Intent: wire order | **PASS** — intent records prepended in ChangeSet |
| `TestDeleteIntentRemovesFromProjection` | **Arch: render path** | **PASS** — deleteIntent cleans up projection |
| `TestIntentToStep_NodeLevel` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_InterfaceLevel` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_SetupDevice` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_SetupDeviceWithRR` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_ConfigureInterface` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_CreatePortChannel` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_SetProperty` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_CreateVLAN` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_CreateACL` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_AddBGPPeer` | Reconstruction: step params | **PASS** |
| `TestIntentToStep_BindACL` | Reconstruction: step params | **PASS** |
| `TestIntentsToSteps_Ordering` | Reconstruction: topological order | **PASS** |
| `TestIntentsToSteps_FiltersNonActuated` | Reconstruction: state filter | **PASS** |
| `TestNodeServiceIntentsFiltersState` | Intent: state-aware queries | **PASS** |
| `TestValidateIntentDAG_Healthy` | **Arch: DAG integrity** | **PASS** |
| `TestValidateIntentDAG_BrokenBidirectional` | **Arch: DAG integrity** | **PASS** |
| `TestValidateIntentDAG_DanglingParent` | **Arch: DAG integrity** | **PASS** |
| `TestValidateIntentDAG_Orphan` | **Arch: DAG integrity** | **PASS** |
| `TestWriteIntent_ParentExistence` | Intent: parent validation | **PASS** |
| `TestWriteIntent_IdempotentUpdate` | **Arch: idempotency** | **PASS** |
| `TestWriteIntent_DifferentParentsError` | Intent: parent validation | **PASS** |
| `TestWriteIntent_ChildRegistered` | Intent: DAG maintenance | **PASS** |
| `TestDeleteIntent_RefusesWithChildren` | Intent: cascade prevention | **PASS** |
| `TestDeleteIntent_DeregistersFromParent` | Intent: DAG maintenance | **PASS** |
| `TestWriteIntent_MultiParent` | Intent: multi-parent DAG | **PASS** |
| `TestIntentsToSteps_TopologicalOrder` | Reconstruction: ordering | **PASS** |
| `TestValidateIntentDAG_BidirectionalInconsistency` | **Arch: DAG integrity** | **PASS** |
| `TestValidateIntentDAG_OrphanDetection` | **Arch: DAG integrity** | **PASS** |
| `TestIntentsByPrefix` | Intent: query helpers | **PASS** |
| `TestIntentsByParam` | Intent: query helpers | **PASS** |
| `TestIntentsByOp` | Intent: query helpers | **PASS** |

**Verdict:** 38 tests, 0 issues. Core architectural compliance — validates intent
DB as primary state, round-trip completeness, DAG integrity, and the intent query
helpers that support intent-based reads.

---

### 15. `pkg/newtron/network/node/interface_ops_test.go` — Interface Operations

Architecture §4 (config method contract), operational symmetry.

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestRemoveService_L3_Basic` | Ops: service removal | **PASS** — tests teardown produces correct entries |
| `TestRemoveService_SharedACL_LastUser` | Ops: shared resource cleanup | **PASS** — reference-aware reverse |
| `TestRemoveService_SharedACL_NotLastUser` | Ops: shared resource lifecycle | **PASS** — preserves shared ACL |
| `TestSetIP` | Ops: interface config | **PASS** |
| `TestSetIP_VRFBound` | Ops: interface config | **PASS** |
| `TestSetIP_Invalid` | Ops: validation | **PASS** |
| `TestSetVRF` | Ops: VRF binding | **PASS** |
| `TestSetVRF_NotFound` | Ops: precondition | **PASS** |
| `TestBindACL` | Ops: ACL binding | **PASS** |
| `TestBindACL_EmptyBindingList` | Ops: ACL binding | **PASS** |
| `TestAddBGPPeer` | Ops: BGP peer | **PASS** |
| `TestRemoveBGPPeer` | Ops: BGP peer removal | **PASS** |
| `TestInterface_NotConnected` | Precondition: connectivity | **PASS** |
| `TestInterface_PortChannelMemberBlocksConfig` | Precondition: membership | **PASS** |
| `TestApplyService_AlreadyBound` | Ops: idempotency | **PASS** |
| `TestRemoveService_NoServiceBound` | Ops: error paths | **PASS** |
| `TestRoundTrip_ConfigureUnconfigureInterface_Routed` | **Arch: operational symmetry** | **PASS** — forward + reverse = clean |
| `TestRoundTrip_AddRemoveBGPPeer` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_BindUnbindACL` | **Arch: operational symmetry** | **PASS** |

**Verdict:** 19 tests, 0 issues. Tests config method contract, reference counting,
and operational symmetry.

---

### 16. `pkg/newtron/network/node/qos_test.go` — QoS Config Generation

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestGenerateDeviceQoSConfig_TwoQueue` | Config gen: QoS | **PASS** — deterministic entry generation |
| `TestGenerateDeviceQoSConfig_EightQueueWithECN` | Config gen: QoS | **PASS** |
| `TestGenerateDeviceQoSConfig_NoECN` | Config gen: QoS | **PASS** |
| `TestQoSBinding` | Ops: QoS binding | **PASS** |
| `TestDSCPDefaultMapping` | Config gen: DSCP | **PASS** |

**Verdict:** 5 tests, 0 issues.

---

### 17. `pkg/newtron/network/node/service_gen_test.go` — Service Config Generation

Architecture §4 (config generators are pure functions), policy lifecycle,
content-hashed naming, operational symmetry.

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestServiceConfig_EVPNBridged` | Config gen: evpn-bridged | **PASS** |
| `TestServiceConfig_Routed_NoVRF` | Config gen: routed | **PASS** |
| `TestServiceConfig_EVPNRouted_WithVRF` | Config gen: evpn-routed | **PASS** |
| `TestServiceConfig_EVPNIRB` | Config gen: evpn-irb | **PASS** |
| `TestServiceConfig_ACL_WithCoS` | Config gen: ACL + CoS | **PASS** |
| `TestServiceConfig_BGP_UnderlayASN` | Config gen: BGP ASN | **PASS** |
| `TestServiceConfig_BGP_FallbackToLocalAS` | Config gen: BGP fallback | **PASS** |
| `TestServiceConfig_BGP_AdminStatus` | Config gen: BGP status | **PASS** |
| `TestServiceConfig_RouteTargets` | Config gen: route targets | **PASS** |
| `TestServiceConfig_SharedVRF` | Config gen: shared VRF | **PASS** |
| `TestServiceConfig_BGP_PeerASRequest` | Config gen: peer AS | **PASS** |
| `TestCreateRoutePolicy_ContentHashedNames` | **Arch: content hashing** | **PASS** |
| `TestCreateRoutePolicy_DifferentContentDifferentHash` | **Arch: content hashing** | **PASS** |
| `TestCreateRoutePolicy_SameContentSameHash` | **Arch: content hashing** | **PASS** |
| `TestCreateRoutePolicy_MerkleHashCascade` | **Arch: Merkle hashing** | **PASS** |
| `TestCreateRoutePolicy_WithCommunity` | Config gen: community | **PASS** |
| `TestCreateInlineRoutePolicy_ContentHashedNames` | **Arch: content hashing** | **PASS** |
| `TestCreateHashedPrefixSet_ContentHash` | **Arch: content hashing** | **PASS** |
| `TestCreateRoutePolicy_ExtraCommunityAndPrefixList` | Config gen: complex | **PASS** |
| `TestScanExistingRoutePolicies_OfflineMode` | Ops: offline scan | **PASS** |
| `TestRefreshService_CleansUpStaleRoutePolicies` | **Arch: blue-green migration** | **PASS** |
| `TestRefreshService_NoStaleCleanupWhenHashUnchanged` | **Arch: blue-green migration** | **PASS** |
| `TestRefreshService_PreservesTopologyParams` | **Arch: topology params** | **PASS** |
| `TestBlueGreenPolicyMigration_TwoInterfaces` | **Arch: blue-green migration** | **PASS** |
| `TestBGPPeerGroup_CreateOnFirst_DeleteOnLast` | **Arch: shared resource lifecycle** | **PASS** |
| `TestScanRoutePoliciesByPrefix_FindsHashedNames` | Ops: route policy scan | **PASS** |

**Verdict:** 26 tests, 0 issues. Validates config generators as pure functions,
content-hashed naming, blue-green migration, and shared resource lifecycles.

---

### 18. `pkg/newtron/network/node/reconstruct_test.go` — Intent Reconstruction (ReplayStep)

Architecture §2 (one pipeline), §3 (construction via ReplayStep).

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestParseStepURL` | Reconstruction: URL parsing | **PASS** |
| `TestParamString` | Reconstruction: param extraction | **PASS** |
| `TestParamInt` | Reconstruction: param extraction | **PASS** |
| `TestParamBool` | Reconstruction: param extraction | **PASS** |
| `TestParamStringMap` | Reconstruction: param extraction | **PASS** |
| `TestParamStringSlice` | Reconstruction: param extraction | **PASS** |
| `TestReplayStepAddBGPEVPNPeer` | **Arch: intent reconstruction** | **PASS** — replays intent, verifies projection |
| `TestReplayStepUnknownOp` | Reconstruction: error paths | **PASS** |
| `TestReplayStepMissingInterface` | Reconstruction: error paths | **PASS** |
| `TestReplayStepSetProperty` | **Arch: intent reconstruction** | **PASS** |
| `TestReplayStepConfigureInterface` | **Arch: intent reconstruction** | **PASS** |
| `TestReplayStepConfigureInterfaceBridged` | **Arch: intent reconstruction** | **PASS** |
| `TestUnconfigureInterfaceBridged` | **Arch: reconstruction reverse** | **PASS** |
| `TestUnconfigureInterfaceRouted` | **Arch: reconstruction reverse** | **PASS** |
| `TestParseRouteReflectorOpts` | Reconstruction: param parsing | **PASS** |

**Verdict:** 15 tests, 0 issues. Validates the reconstruction path
(IntentsToSteps → ReplayStep).

---

### 19. `pkg/newtron/network/node/precondition_external_test.go` — Intent-Based Preconditions

Architecture §1: "All operational decisions read the intent DB."

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestPreconditionChecker_RequireConnected_Pass` | Precondition: connectivity | **PASS** |
| `TestPreconditionChecker_RequireConnected_Fail` | Precondition: connectivity | **PASS** |
| `TestPreconditionChecker_RequireLocked_Pass` | Precondition: locking | **PASS** |
| `TestPreconditionChecker_RequireLocked_Fail` | Precondition: locking | **PASS** |
| `TestPreconditionChecker_ChainedChecks_AllPass` | Precondition: chaining | **PASS** |
| `TestPreconditionChecker_ChainedChecks_MultipleFailures` | Precondition: aggregation | **PASS** |
| `TestPreconditionChecker_RequireVLANExists_Pass` | **Arch: intent-based precondition** | **PASS** — populates `NewtronIntent`, not projection |
| `TestPreconditionChecker_RequireVLANExists_Fail` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequireVLANNotExists_Pass` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequireVLANNotExists_Fail` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequireVRFExists` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequirePortChannelExists` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequireACLTableExists` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequireVTEPConfigured` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_RequireInterfaceNotPortChannelMember` | **Arch: intent-based precondition** | **PASS** |
| `TestPreconditionChecker_CustomCheck` | Precondition: extensibility | **PASS** |
| `TestPreconditionChecker_NoErrors` | Precondition: baseline | **PASS** |

**Verdict:** 17 tests, 0 issues. All entity preconditions correctly populate
`NewtronIntent` (not projection tables) — validates §1 "intent DB is the decision
substrate." Comments in the test code explicitly state this: "Preconditions check
the intent DB, not the projection."

---

### 20. `pkg/newtron/network/node/device_ops_test.go` — CRUD Operations & Round-Trips

Architecture §4 (config method contract), operational symmetry,
intent idempotency.

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestCreateVLAN_Basic` | CRUD: VLAN create | **PASS** |
| `TestCreateVLAN_WithL2VNI` | CRUD: VLAN + EVPN | **PASS** |
| `TestCreateVLAN_IntentIdempotent` | **Arch: intent idempotency** | **PASS** — second call returns empty ChangeSet |
| `TestDeleteVLAN_WithMembers` | CRUD: VLAN delete | **PASS** |
| `TestCreatePortChannel_Basic` | CRUD: PortChannel create | **PASS** |
| `TestAddPortChannelMember` | CRUD: PC member add | **PASS** |
| `TestRemovePortChannelMember` | CRUD: PC member remove | **PASS** |
| `TestCreateVRF_Basic` | CRUD: VRF create | **PASS** |
| `TestDeleteVRF_NoInterfaces` | CRUD: VRF delete | **PASS** |
| `TestDeleteVRF_BoundInterfacesBlocks` | Precondition: VRF in use | **PASS** |
| `TestCreateACL_Basic` | CRUD: ACL create | **PASS** |
| `TestDeleteACL_RemovesRules` | CRUD: ACL cascade delete | **PASS** |
| `TestUnbindACLFromInterface` | CRUD: ACL unbind | **PASS** |
| `TestAddACLRule` | CRUD: ACL rule | **PASS** |
| `TestBindMACVPN` | CRUD: MAC-VPN bind | **PASS** |
| `TestConfigureIRB` | CRUD: IRB config | **PASS** |
| `TestDevice_NotConnected` | Precondition: connectivity | **PASS** |
| `TestDevice_NotLocked` | Precondition: locking | **PASS** |
| `TestCreateVLAN_InvalidID` | Validation: range | **PASS** |
| `TestRoundTrip_CreateDeleteVLAN` | **Arch: operational symmetry** | **PASS** — create + delete = clean intent DB |
| `TestRoundTrip_CreateDeleteVRF` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_CreateDeleteACL` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_CreateDeletePortChannel` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_AddRemoveBGPEVPNPeer` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_ConfigureUnconfigureIRB` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_AddRemoveStaticRoute` | **Arch: operational symmetry** | **PASS** |
| `TestRoundTrip_BindUnbindMACVPN` | **Arch: operational symmetry** | **PASS** |

**Verdict:** 27 tests, 0 issues. Validates config method contract, intent
idempotency, and operational symmetry (forward + reverse = clean state).

---

### 21. `pkg/newtron/network/node/types_test.go` — ChangeSet + Domain Types

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestChangeTypeConstants` | Types: ChangeSet | N/A |
| `TestChange_Structure` | Types: ChangeSet | N/A |
| `TestChange_NilValues` | Types: ChangeSet | N/A |
| `TestNewChangeSet` | Types: ChangeSet | N/A |
| `TestChangeSet_Timestamp` | Types: ChangeSet | N/A |
| `TestChangeSet_Add` | Types: ChangeSet | N/A |
| `TestChangeSet_AddMultiple` | Types: ChangeSet | N/A |
| `TestChangeSet_IsEmpty` | Types: ChangeSet | N/A |
| `TestChangeSet_String_Empty` | Types: display | N/A |
| `TestChangeSet_String_WithChanges` | Types: display | N/A |
| `TestChangeSet_String_ShowsNewValue` | Types: display | N/A |
| `TestChangeSet_Preview` | Types: display | N/A |
| `TestVLANInfo_Structure` | Types: VLANInfo | N/A — data type |
| `TestVLANInfo_L2VNI` | Types: VLANInfo | N/A |
| `TestMACVPNInfo_Structure` | Types: MACVPNInfo | N/A |
| `TestVRFInfo_Structure` | Types: VRFInfo | N/A |
| `TestPortChannelInfo_Structure` | Types: PCInfo | N/A |
| `TestInterface_TypeDetection` | Types: interface types | N/A |
| `TestInterface_Name` | Types: interface | N/A |
| `TestInterface_Properties` | **Arch: intent-based reads** | **PASS** — reads VRF/IP from `NewtronIntent`, port props from PORT table (correct per architecture) |
| `TestInterface_HasService` | **Arch: intent-based reads** | **PASS** — reads service binding from `NewtronIntent` |
| `TestInterface_ServiceBindingProperties` | **Arch: intent-based reads** | **PASS** |
| `TestInterface_PortChannelMembership` | **Arch: intent-based reads** | **PASS** — reads from `NewtronIntent`, not `PortChannelMember` |
| `TestInterface_String` | Types: display | N/A |
| `TestNode_Name` | Types: node | N/A |
| `TestNode_IsConnected_NotConnected` | Types: node state | N/A |
| `TestNode_IsLocked_NotLocked` | Types: node state | N/A |

**Verdict:** 27 tests, 0 issues. Key tests (`TestInterface_Properties`,
`TestInterface_HasService`, `TestInterface_PortChannelMembership`) correctly
populate `NewtronIntent` for intent-based reads — validates the intent-based
reads architecture.

---

### 22. `pkg/newtron/network/network_test.go` — Spec Resolution

| Test Function | Category | Architectural Compliance |
|---------------|----------|------------------------|
| `TestNetwork_ListServicesEmpty` | Network: spec access | N/A |
| `TestNetwork_ListFiltersEmpty` | Network: spec access | N/A |
| `TestResolvedSpecs_MergeNodeWins` | Spec: hierarchical merge | **PASS** — node overrides network |
| `TestResolvedSpecs_MergeZoneWinsOverNetwork` | Spec: hierarchical merge | **PASS** |
| `TestResolvedSpecs_MergeUnion` | Spec: hierarchical merge | **PASS** |
| `TestResolvedSpecs_FindMACVPNByVNI` | Spec: VNI lookup | **PASS** |
| `TestResolvedSpecs_FindMACVPNByVNI_DynamicFallback` | Spec: dynamic fallback | **PASS** |
| `TestResolvedSpecs_LiveFallback_DynamicService` | Spec: live fallback | **PASS** |
| `TestResolvedSpecs_LiveFallback_ProfileOverrideStillWins` | Spec: override precedence | **PASS** |
| `TestResolvedSpecs_GetPlatformDelegatesToNetwork` | Spec: platform delegation | **PASS** |

**Verdict:** 10 tests, 0 issues. Validates "Definition Is Network-Scoped;
Execution Is Device-Scoped" principle.

---

### 23. `pkg/newtron/network/node/architecture_test.go` — Architecture Compliance

Tests that verify structural invariants of the intent-first architecture.
Added to close gaps M1-M6 (unit-testable subset of M1-M10).

| # | Function | Category | Architecture Reference |
|---|----------|----------|----------------------|
| 1 | `TestRebuildProjection_PreservesIntentDB` | M1 — RebuildProjection freshness | §1, §8: intents survive rebuild |
| 2 | `TestRebuildProjection_PreservesPorts` | M1 — RebuildProjection freshness | §8: ports preserved across rebuild |
| 3 | `TestRebuildProjection_ClearsStaleProjection` | M1 — RebuildProjection freshness | §1: stale projection entries discarded |
| 4 | `TestNewAbstract_EmptyProjection` | M2 — Projection not loaded from device | §11: NewAbstract starts empty |
| 5 | `TestNewAbstract_NoTransport` | M2 — Projection not loaded from device | §7: no transport on construction |
| 6 | `TestLock_NoOpWithoutTransport` | M4 — Lock no-op | §8: Lock/Apply/Unlock no-ops without transport |
| 7 | `TestModeProperties_TopologyMode` | M5 — Mode properties | §3: topology mode flags |
| 8 | `TestUnsavedIntents_SetByWriteIntent` | M5 — unsavedIntents tracking | §3: writeIntent sets flag |
| 9 | `TestUnsavedIntents_ClearedByClear` | M5 — unsavedIntents tracking | §3: ClearUnsavedIntents resets flag |
| 10 | `TestUnsavedIntents_ClearedAfterReplay` | M5 — unsavedIntents lifecycle | §3: BuildAbstractNode pattern (replay + clear) |
| 11 | `TestActuatedIntent_FlagOnConstruction` | M5 — actuatedIntent flag | §3: flag set at construction |
| 12 | `TestSnapshotRestore_IntentDBPreserved` | M6 — Dry-run snapshot/restore | §8: snapshot/restore preserves intent DB |
| 13 | `TestSnapshotRestore_DeepCopy` | M6 — Snapshot isolation | §8: snapshot is a deep copy |
| 14 | `TestSnapshotRestore_ProjectionLeftDirty` | M6 — Restore semantics | §8: RestoreIntentDB leaves projection dirty |
| 15 | `TestDisconnectTransport_PreservesProjection` | M3 partial — Transport preservation | §7: DisconnectTransport preserves projection |
| 16 | `TestRender_UpdatesProjectionOnEveryConfigMethod` | Render correctness | §5: render runs on every path |
| 17 | `TestTree_ReadsIntentDB` | Tree operation | §6: Tree reads intent DB |
| 18 | `TestIntentDB_PreconditionsUseIntents` | Intent-based decisions | §1: idempotency and preconditions via GetIntent |
| 19 | `TestConfigMethod_UpdatesIntentAndProjection` | Config method contract | §4: by return, intent DB and projection both updated |

**Coverage:** 19 tests covering M1, M2, M3 (partial), M4 (partial), M5, M6,
plus render correctness, Tree, preconditions, and config method contracts.

**Not covered (require mock transport):** M3 full (ConnectTransport), M4 full
(drift guard), M7 (Reconcile), M8 (Drift), M9 (Save), M10 (Reload/Clear).

---

## Dead Tests

No dead tests found. The previously deleted tests (`TestSplitConfigDBKey`,
`TestExtractServiceFromACL` — Finding 34/35 from function-audit.md) were
removed in the prior audit session.

---

## Missing Architectural Compliance Tests

These are architectural properties defined in the unified pipeline architecture
that have no corresponding test. They represent gaps in the test suite's ability
to catch regressions against the architecture.

### ~~M1. RebuildProjection freshness guarantee~~ — COVERED

Added in `architecture_test.go`: `TestRebuildProjection_PreservesIntentDB`,
`TestRebuildProjection_PreservesPorts`, `TestRebuildProjection_ClearsStaleProjection`.

### ~~M2. Projection never loaded from device~~ — COVERED

Added in `architecture_test.go`: `TestNewAbstract_EmptyProjection`,
`TestNewAbstract_NoTransport`.

### M3. ConnectTransport projection preservation — PARTIALLY COVERED

`TestDisconnectTransport_PreservesProjection` covers the disconnect path.
Full `ConnectTransport` test requires mock SSH+Redis.

### M4. Drift guard in `Lock` — PARTIALLY COVERED

`TestLock_NoOpWithoutTransport` covers the no-transport path.
Full drift guard test requires mock Redis client returning divergent CONFIG_DB.

### ~~M5. Mode switching properties~~ — COVERED (unit-testable parts)

Added in `architecture_test.go`: `TestModeProperties_TopologyMode`,
`TestUnsavedIntents_SetByWriteIntent`, `TestUnsavedIntents_ClearedByClear`,
`TestUnsavedIntents_ClearedAfterReplay`, `TestActuatedIntent_FlagOnConstruction`.
Full mode switching with transport requires mock transport.

### ~~M6. Execute dry-run + intent restore~~ — COVERED

Added in `architecture_test.go`: `TestSnapshotRestore_IntentDBPreserved`,
`TestSnapshotRestore_DeepCopy`, `TestSnapshotRestore_ProjectionLeftDirty`.

### M7. Reconcile operation — NOT TESTED

Architecture §6: Reconcile delivers the full projection to the device via
`ExportEntries()` + `ReplaceAll()`. No unit test for the `Reconcile()` method.
(Would require a mock transport.)

### M8. No test for `Drift` operation end-to-end

Architecture §6: Drift compares `configDB.ExportRaw()` against
`conn.GetRawOwnedTables()`. While `DiffConfigDB` is well-tested (§8.
configdb_diff_test.go), no test exercises the full `Node.Drift(ctx)` method
that calls `ConnectTransport` + `ExportRaw` + `GetRawOwnedTables` + `DiffConfigDB`.

### M9. No test for `Save` operation

Architecture §6: Save reads the intent DB via `Tree()`, then persists to
`topology.json`. No unit test for the `Save` / `SaveDeviceIntents` path.

### M10. No test for `Reload` / `Clear` operations

Architecture §6: Reload discards unsaved changes and rebuilds from
topology.json. Clear creates an empty node. No tests.

---

## Summary

| Metric | Count |
|--------|-------|
| Total test functions | 370 |
| Tests with no architecture relevance | 109 |
| Tests validating architectural compliance | 261 |
| Dead tests | 0 |
| Failing architectural compliance | 0 |
| Missing architectural tests | 4 (M7-M10, require mock transport) |

### Architectural Coverage by Principle

| Architecture Section | Test Coverage |
|---------------------|---------------|
| §1 Intent DB is primary state | **Covered** — intent_test.go (38 tests), precondition_external_test.go (17 tests), types_test.go (4 tests), architecture_test.go (preconditions, config method contract) |
| §2 One Pipeline (render) | **Covered** — schema_test.go (44 tests) validates render guard; intent_test.go tests writeIntent/deleteIntent; architecture_test.go `TestRender_UpdatesProjectionOnEveryConfigMethod` |
| §3 Three States | **Partially covered** — architecture_test.go mode properties (5 tests: actuatedIntent, unsavedIntents, replay lifecycle). Missing: full mode switching with transport (M5 full) |
| §4 Config Methods | **Covered** — device_ops_test.go (27 tests), interface_ops_test.go (19 tests), service_gen_test.go (26 tests), architecture_test.go `TestConfigMethod_UpdatesIntentAndProjection` |
| §5 Rendering | **Covered** — schema validation tested; render(cs) called by all config method tests; architecture_test.go verifies render on multiple operations |
| §6 Six Operations | **Partially covered** — Tree (architecture_test.go), Drift primitives (configdb_diff_test.go). Missing: Reconcile (M7), Save (M9), Reload/Clear (M10) — require mock transport |
| §7 Device I/O | **Partially covered** — architecture_test.go `TestDisconnectTransport_PreservesProjection`. Missing: full ConnectTransport (M3) — requires mock transport |
| §8 Lock / RebuildProjection / Execute | **Partially covered** — architecture_test.go: RebuildProjection freshness (3 tests), Lock no-op (1 test), snapshot/restore (3 tests). Missing: drift guard (M4 full) — requires mock transport |
| §9 Data Representations | **Covered** — configdb_parsers_test.go (10 tests), configdb_consistency_test.go (4 tests) |
| §10 End-to-End Traces | **Partially covered** — reconstruct_test.go (15 tests) covers replay path |
| §11 What Changed | **Covered** — architecture_test.go `TestNewAbstract_EmptyProjection` + `TestNewAbstract_NoTransport` verify projection not loaded from device |

### Conclusion

The test suite covers all offline/unit-testable architectural properties:
intent DB operations, config method contracts, schema validation, operational
symmetry, content-hashed naming, intent-based reads, RebuildProjection freshness,
mode properties, snapshot/restore semantics, Lock no-op, DisconnectTransport
preservation, render correctness, and Tree.

The remaining 4 gaps (M7-M10) require mock transport implementations:
Reconcile end-to-end, Drift end-to-end, Save to topology.json, and
Reload/Clear from topology.json. The drift guard in Lock (M4 full) and
ConnectTransport preservation (M3 full) also need mock transport for
complete coverage.
