# Documentation Editing Guidelines

Principles for writing and editing newtron project documentation, captured from the HLD, LLD, HOWTO, and API reference rewrites (March 2026).

**Scope key:** Each guideline is tagged with the document types it applies to.

| Tag | Applies to |
|-----|-----------|
| ALL | HLD, LLD, Device LLD, HOWTO, README, API |
| HLD | High-Level Design |
| LLD | Low-Level Design, Device LLD |
| HLD+LLD | Both design documents |
| HOWTO | Operational guides (howto.md) |
| README | Project and package READMEs |
| API | HTTP API reference (api.md) |

## 1. Examples Must Be Type-Valid — ALL

If you show a JSON spec with `service_type: "routed"`, every other field must be valid for that type. Don't mix features from different service types to make a richer example — use the correct type that actually supports those features.

**Bad:** `service_type: "routed"` with `ipvpn: "customer-vpn"` (ipvpn requires evpn-routed or evpn-irb)
**Good:** `service_type: "evpn-routed"` with `ipvpn: "customer-vpn"`

This applies to all examples: JSON specs, CONFIG_DB entries, CLI commands, code snippets. Every example is a testable claim about the system.

HOWTOs are especially vulnerable — they accumulate examples over time, and a schema change can silently invalidate dozens of snippets.

## 2. Precision Before Readability — ALL

When simplifying or restructuring for clarity, verify that the simplified version is still technically correct. Restructuring introduces semantic errors that are invisible to the writer because the new prose "reads well."

If an example needs to be simpler, simplify by removing fields — not by changing types or mixing incompatible features.

## 3. Architecture Diagrams Show Relationships, Not Just Boxes — HLD

Containment (NetworkActor manages NodeActors), data flow direction (Network Layer → Node Layer via `Connect()`), and ownership (who holds what reference) must be visible in the diagram. Parallel boxes with no connecting lines misrepresent the architecture.

When drawing a diagram, ask:
- Which component creates/owns which?
- What references does each component hold?
- What is the data flow direction between components?
- Are there two paths that converge? Show where they meet.

## 4. Each Concept Explained Exactly Once — HLD+LLD

Pick the canonical section for a concept and explain it there. Every other section references it, never re-explains it. A glossary may define terms tersely but should not duplicate the prose explanation.

When the same concept appears in multiple sections with slightly different nuance, none is authoritative and the reader cannot tell which to trust.

HOWTOs and READMEs may restate concepts for self-containment, but should link to the canonical explanation and never contradict it.

## 5. HLD Covers What and Why; LLD Covers How and What Fields — HLD+LLD

| Belongs in HLD | Belongs in LLD |
|----------------|----------------|
| Component responsibilities | CONFIG_DB table schemas and key formats |
| Design decisions and trade-offs | Extended field lists (BGP_GLOBALS, BGP_NEIGHBOR_AF) |
| Object hierarchy and relationships | Full CLI command tree with all flags |
| Service types and their requirements | QoS CONFIG_DB derivation tables |
| Verification tiers and ownership | Permission level tables |
| Actor model and dispatch patterns | SSH tunnel implementation details |

The HLD should let a reader understand the system's architecture without ever mentioning a CONFIG_DB field name (except in examples that illustrate the spec→config translation).

**HOWTO** covers neither what/why nor how/what-fields — it covers **when and in what order**. Operational procedures, step sequences, troubleshooting flows.

**README** covers **what this is and how to get started** — installation, first run, pointer to further docs.

## 6. End-to-End Walkthrough Ties It Together — HLD+LLD

After presenting architecture, layers, and types separately, trace one concrete operation through the entire stack. This is where the reader verifies their mental model matches reality.

A good walkthrough:
- Starts from user action (CLI command)
- Shows every layer the request passes through
- Names the actual functions/methods called
- Shows the final effect (Redis write)
- Shows the response path back to the user

Include both a write path (device operation) and a read path (spec query) to show how the two actor types differ.

The HLD walkthrough names layers and responsibilities. The LLD walkthrough names functions and data types.

## 7. Audit After Rewrite — ALL

After any major doc restructure, run a precision audit against the code. Check:
- Every example's field combinations against the type system
- URL patterns against actual route registration
- Method signatures against actual code
- Lifecycle sequences (Lock→fn→Commit→Save→Unlock) against implementation
- Claims about which component owns what against actual struct fields

Restructuring moves content between sections and rewrites prose. Both operations introduce semantic drift that the writer doesn't notice because they're focused on structure.

HOWTOs need auditing too — CLI flag names, command syntax, and step sequences all drift when the code changes.

## 8. Reference Sections Need Context, Not Just Headers — LLD

A section header followed immediately by a type definition tells the reader nothing about when, where, or why that type appears. Every group of type definitions, route tables, or schema entries needs one sentence connecting it to the rest of the system.

**Bad:** `### 3.3 Resource Views` → `type VLANStatusEntry struct {`
**Good:** `### 3.3 Resource Views` → "Returned by the node read endpoints in §4.5." → `type VLANStatusEntry struct {`

This applies to all reference material: types, routes, CONFIG_DB tables, CLI commands. The sentence answers "where does this fit?" so the reader isn't left cross-referencing manually.

HLDs rarely have this problem (they're narrative-driven). HOWTOs rarely have reference sections. This is primarily an LLD concern.

## 9. Cross-Reference Between Sections — LLD

When types and the routes/endpoints that use them live in separate sections, explicitly link them. The reader shouldn't have to guess which request type belongs to which endpoint.

- Type sections should say which endpoint sections use them ("Used as request bodies for §4.6")
- Endpoint sections should say which type sections define their contracts ("Request/response types from §3.6–3.7")

A document with correct content but no cross-references forces the reader to hold the whole structure in their head. Cross-references let them navigate.

HLDs connect concepts through narrative flow. LLDs need explicit cross-references because their sections are self-contained reference entries.

## 10. Worked Examples Tie Implementation Sections Together — LLD

An LLD can list every type signature, every route, every CONFIG_DB table, and still fail to explain how the pieces connect. A worked example that traces one concrete operation through every layer — from HTTP request to Redis write to JSON response — is the single most valuable addition.

A good worked example:
- Picks a simple, representative operation (CreateVLAN, not ApplyService)
- Shows the exact function calls at each layer, not just the layer names
- Shows data transformation (JSON body → Go struct → Entry → Redis HSET → WriteResult → JSON response)
- Annotates each step with what it does and why

The worked example is where the reader verifies that their mental model of the architecture (from the HLD) matches the actual mechanics (in the LLD).

HLDs have walkthroughs (§6) but at the architecture level, not the function-call level. HOWTOs have step sequences but at the user-action level, not the implementation level. The LLD worked example operates at the code level.

## 11. Document What Is, Not What's Intended — ALL

When code defines types, constants, or interfaces that aren't wired up yet, the document must state the current enforcement status. Describing intended behavior as current behavior is a lie that readers will trust and act on.

**Bad:** "Write permissions checked per-operation." (when no auth middleware exists)
**Good:** "Permission types are defined for future enforcement. The server has no authentication middleware — it is designed for trusted-network deployment."

Separate "exists in code" from "enforced at runtime." If a permission system has types but no enforcement, say both things.

This applies everywhere. HOWTOs that say "run X to enable Y" when Y isn't actually wired up are worse than LLD inaccuracies — users will follow the steps and wonder why nothing works.

## 12. The Difference Between Good and Great Is Connective Tissue — ALL

The reference material (types, routes, schemas) doesn't change between a 7/10 and a 10/10 document. What changes is:
- Intro paragraphs that give the reader a mental model before the details
- Context sentences that say where each piece fits
- Cross-references between sections
- Worked examples that trace one operation end-to-end
- Honest status notes for unfinished features

The content is always the same. The 10/10 version just tells you how to read it.

This applies to every document type, but manifests differently:
- **HLD:** intro paragraph before each architecture section
- **LLD:** context sentence before each type/route group
- **HOWTO:** each procedure needs all five: (1) why you'd do this, (2) preconditions, (3) step sequence with commands, (4) example output, (5) what can go wrong. A section that shows a command without output or context is a stub, not a procedure. See also §25.
- **README:** "what problem this solves" before installation steps

A structurally correct document with thin sections is not a 7/10 that needs polish — it's a 5/10 that's missing content. Structure without depth is an outline, not documentation.

## 13. Architecture Changes Require Full Rewrites, Not Patches — ALL

When the fundamental mental model changes — direct SSH connections become HTTP API calls, a monolith splits into client/server, a synchronous flow becomes async — patching the existing document produces inconsistent text. The old assumptions leak through in verb choices ("connect to the device"), data flow descriptions ("the CLI reads CONFIG_DB"), and implicit context ("the Node object holds an SSH session").

Start from scratch. Gather ground truth from source code, write the new document against reality, then audit. Patching is appropriate for incremental changes (new flag, renamed field). It is not appropriate when the document's mental model is wrong.

Signs you need a full rewrite rather than patches:
- The subject of most sentences has changed (CLI → server, Node → client)
- The data flow direction has reversed or gained a hop
- More than half the examples need updating
- You keep finding "just one more" stale assumption as you patch

**"Start from scratch" means rewrite the prose, not discard the content.** When the architecture changes (direct → client/server), the framing changes but operations survive. Precondition tables, step-by-step sequences, troubleshooting recipes, worked workflows, CLI output examples — these are independent of the connection model. `vrf add-neighbor` still takes the same arguments and has the same auto-derivation rules whether the CLI talks SSH or HTTP.

Before writing a single line, inventory the existing document's operational content:
- Count subsections per major section
- Count precondition tables, worked examples, troubleshooting entries
- Count CLI commands shown and CLI output examples
- List the end-to-end workflows

The rewrite must match or exceed these counts. A rewrite that produces a correct skeleton with half the operational depth removed is worse than the stale original — the stale original at least contains the knowledge operators need. Getting the architecture diagram right is the easy part; preserving operational depth under a new mental model is the actual work.

## 14. Section Order Follows Operational Frequency — HOWTO

Arrange sections by how often users need them, not by architectural hierarchy or alphabetical order. In a HOWTO, the most common operation goes first. Users scan the table of contents to find what they need — the thing they need most should be near the top.

For newtron: services are the primary entry point (most users run `service apply` far more than `vlan create`), so Services comes before the building blocks (Interface, VLAN, VRF) that services create automatically. Building blocks come before advanced topics (ACL, QoS, Filters). Troubleshooting and reference material go last.

This ordering also teaches the right mental model: "services are the high-level operation; VLANs and VRFs are implementation details you rarely manage directly."

HLDs and LLDs follow architectural dependency order (define X before using X). HOWTOs follow frequency order (show what users do most, first).

## 15. Unenforced Features Must Not Appear in HOWTO Examples — HOWTO

Guideline #11 says to separate "exists in code" from "enforced at runtime." For HOWTOs, the rule is stronger: **don't show unenforced features at all.** Users copy examples verbatim. A `network.json` example with `"permissions": {...}` will be pasted into real configs, and when the permissions have no effect, the user blames themselves rather than the documentation.

An LLD may document unenforced types with honest status notes ("defined for future enforcement; no auth middleware exists"). A HOWTO must not — it shows only what works today.

**Bad (HOWTO):**
```json
{
  "permissions": { "admin": "readwrite", "operator": "read" },
  "nodes": { ... }
}
```

**Good (HOWTO):** Omit `permissions` entirely. If users ask, point them to the LLD's honest status note.

This is not about hiding features — it's about not teaching users to rely on things that don't work. The HOWTO is a trust contract: "if you follow these steps, this outcome will happen."

## 16. Exhaustive Listing Is the Enemy of Clarity — API

When multiple endpoints follow the same pattern (list/show pairs, CRUD families), state the pattern once and put the instances in a table. Expand only the endpoints with distinctive behavior.

**Bad:** Twenty identical sections that each say "List all X" / "Show a single X" with the same path parameter table, the same status codes, the same response shape.

**Good:** One paragraph explaining the list/show contract, one table mapping resource → path → response type, then expanded entries only for endpoints that break the pattern (host profile returns 404 for switches, route-policy returns `[]string` not full objects).

The reader who skims a 20-endpoint section learns nothing from any individual entry. The reader who scans a table sees all 20 at once and spots the pattern immediately.

## 17. Audit Catches What Self-Review Cannot — ALL

The writer's mental model of the code is unreliable. During the API reference write, the author projected "what the endpoint should return" rather than "what the handler actually returns." A code-side audit caught three semantic errors that survived careful writing and self-review:

- A "neighbor" endpoint was documented as returning ARP/NDP entries, but actually calls `CheckBGPSessions`
- A `CleanupSummary` type was documented as returned by the cleanup endpoint, but the handler discards it
- A delete endpoint was documented as returning 404, but the error type maps to 500

**Rule:** After writing any document that makes claims about code behavior, dispatch a separate audit that reads both the document and the source files side by side. The auditor must have the source code open — not just the document and a memory of the code. Self-review is not a substitute.

## 18. One Example Response Teaches More Than Ten Type Tables — API

A type table (`| field | type | description |`) tells you what fields exist. An example response tells you what the data actually looks like — nesting, realistic values, which fields are populated vs empty, array cardinality.

Every "important" endpoint (the ones users call most, or the ones with complex response shapes) should have at least one example response. "Important" means: service apply (WriteResult with verification), health check (nested checks), BGP status (neighbor array), composite generate/deliver.

Type tables are reference material. Example responses are teaching material. You need both.

## 19. Forward References Need Reverse Cross-References — API+LLD

When an endpoint section says "Response: `WriteResult` (see §14)," the Types Reference section for `WriteResult` should say "Returned by: §8 Node Write Operations, §12 Interface Operations." Without the reverse link, a reader who lands in the Types Reference (from search, from a link) cannot navigate back to the endpoints that produce that type.

This applies to any document with separate "usage" and "definition" sections — LLD type definitions should link back to the code sections that use them, API type definitions should link back to the endpoints that return them.

## 20. API Reference Is a Fourth Document Type — ALL

The API reference is not an LLD appendix or a HOWTO variant. It occupies its own position in the documentation hierarchy:

| Document | Answers | Reader's goal |
|----------|---------|---------------|
| HLD | What and why | Understand architecture |
| LLD | How and what fields | Read/modify the code |
| HOWTO | When and in what order | Operate the system via CLI |
| API | What endpoint, what params, what response | Write an HTTP client |

An API reference that reads like an LLD (too much internal detail) or a HOWTO (too much procedural context) fails its reader. The API reader wants: route, method, parameters, request body, response shape, status codes, and one example. They do not want: why the actor model exists, how the SSH connection is cached, or what CONFIG_DB tables are written.

The API reference should include a workflow section (§2 Typical Workflow) that shows the operational sequence — but this is the API's equivalent of a HOWTO's "getting started," not a replacement for the per-endpoint reference.

## 21. "Array of X" Is a Precision Failure — API

When a response is documented as "Array of `FooDetail`", the reader doesn't know: Is it an array at the top level? Wrapped in `data`? Can it be empty? What order? What's the cardinality — always N, at most 1, unbounded?

**Bad:** "Response: Array of `VLANStatusEntry`"

**Good:** Either show an example response (which answers all questions visually) or specify: "Response: `data` contains an array of `VLANStatusEntry`, one per VLAN on the device. Empty array if no VLANs configured. Ordered by VLAN ID."

For high-frequency endpoints, an example response is strictly better than prose — it's unambiguous and faster to read. For low-frequency endpoints, the brief prose is sufficient.

## 22. Framework Docs: Mechanism First, Inventory Second — HLD

When documenting a framework (test runner, plugin system, patch framework), explain the mechanism — how it works, what users can build with it — before listing the shipped instances. Built-in topologies, suites, patches, and plugins are *examples* of the mechanism, not the mechanism itself.

Structure: §mechanism (YAML schema, action vocabulary, extension points) → §built-in instances (shipped suites, bundled plugins). This way, when the inventory changes, only the examples section is stale. When the document conflates mechanism and inventory, every addition or removal reads like an architecture change.

**Test:** If someone deleted all the built-in suites, would the document still explain how to write one from scratch? If not, the mechanism section is incomplete.

## 23. CLI Output Mockups Must Match Actual Format — HOWTO

When showing example terminal output (status tables, command responses, error messages), derive the format from the formatting code — don't compose it from memory. Guideline #1 covers JSON/YAML examples, but CLI output mockups are a different failure mode: they look plausible, read well, and are never type-checked.

During the newtrun HOWTO rewrite, the `newtrun status` example was missing a `STEPS` column. The mockup had been composed from memory rather than from the actual table header in `cmd_status.go`. It looked correct on casual reading.

**Rule:** Read the formatting code (table headers, `fmt.Sprintf` patterns, `PauseError.Error()` messages) before writing example output. Check:
- Table column names and order
- Field alignment and padding
- Error message text (including what variables are interpolated)
- Status values (the exact strings, not paraphrases)

If the command is runnable, run it and paste. If not (e.g., the output depends on a deployed topology), read the code that formats it.

## 24. Avoid Embedding Counts That Drift — ALL

Don't write "56 actions" or "20 scenarios" in prose unless the number is stable and verifiable. Embedded counts become stale silently — unlike wrong field names (which break copy-paste), wrong counts merely erode trust.

During the newtrun HOWTO rewrite, "56 actions" was accurate for `StepAction` constants but wrong for `newtrun actions` output (55 — one action had no metadata entry). The number looked authoritative, invited no verification, and would have been wrong the moment someone added or removed an action.

**Rule:** Prefer "all actions" or "use `newtrun actions` for the full list" over embedding a specific number. If a count adds genuine value (e.g., "5 built-in health checks" in an architecture discussion), pin it to the source: "5 built-in health checks (see `health_ops.go`)" so a future editor knows where to verify.

## 25. HOWTO Sections Must Be Operationally Complete — HOWTO

A HOWTO section that shows a command without context is a stub. Every noun section (Service, VLAN, VRF, ACL, QoS, etc.) must include:

1. **Why you'd use this** — one sentence situating the operation ("VRFs isolate routing tables per customer; use them when services need separate forwarding domains").
2. **Read operations** — list, show, status commands with example output.
3. **Write operations** — every mutating command with flags, example invocation, and example output (both dry-run and execute).
4. **Preconditions** — what must be true before the operation succeeds ("interface must exist and not be a LAG member"). Precondition tables are ideal.
5. **Operational sequences** — when operations must happen in a specific order, show the full sequence as a worked workflow (create VRF → add interfaces → add neighbors → bind IP-VPN).
6. **What can go wrong** — common precondition failures and their fixes, or a pointer to the Troubleshooting section.

Sections that document many subcommands (VRF has 13, ACL has 9) need proportional depth — not one example each, but enough that an operator can use every subcommand from the HOWTO alone without reading source code.

**Completeness test:** Can an operator who has never used newtron complete the operation described in this section using only the HOWTO? If they need to guess flag names, operation order, or preconditions, the section is incomplete.

This guideline interacts with #13 (rewrites): when rewriting a HOWTO for an architecture change, inventory the existing section depths first. A VRF section that went from 14 subsections to 8, or an ACL section that went from 9 to 4, has lost operational content regardless of whether the new architecture is correct. The rewrite must preserve operational depth.

## 26. Rewrites Must Be Compared Against the Document They Replace — ALL

After completing a rewrite, read the committed version on the main branch side by side with the new version. For every section, paragraph, table, example, and workflow in the original, verify that the new version either:

1. **Preserves it** — the content appears in the rewrite, reframed for the new architecture.
2. **Intentionally removes it** — the content is obsolete (e.g., documents a deleted feature like `shell`). Note the removal and the reason.
3. **Supersedes it** — the rewrite covers the same ground in a better way.

If the original has content that doesn't fall into any of these three categories, the rewrite is incomplete.

This catches the specific failure mode where a rewriter treats the task as greenfield and never opens the original. Operational content that accumulated over months — precondition tables, auto-derivation rules, troubleshooting recipes, worked workflows — gets silently dropped because the rewriter didn't know it existed.

**Process:**
1. Finish the rewrite against the new architecture (per #13).
2. Run `git show main:<path>` and read the committed version end to end.
3. For each section in the original that has more depth than the rewrite, carry the content forward under the new mental model.
4. Apply all other editing guidelines (#1 type-validity, #2 precision, #11 document what is, #25 completeness) to the carried-forward content — don't preserve stale claims just because the original had them.

The goal is the best of both: the new architecture from the rewrite, the operational depth from the original.

## 27. Carry Forward Substance, Not Text — ALL

When guideline #26 requires carrying forward content from an older version, carry
the *insight* — not the paragraph. The old text was written under a different mental
model; transplanting it verbatim creates prose where two mental models coexist.

For each piece of content to preserve:
1. Identify the operational lesson it teaches (a precondition, a gotcha, a sequence)
2. Express that lesson fitted to the new architecture's capabilities and framing
3. Verify the lesson still holds under the new architecture

Sometimes the old phrasing delivers the lesson more powerfully than anything the
rewriter comes up with. When it does, keep the essence of that delivery — the
rhythm, the directness, the concrete image — and fit it to the revised architecture.
The goal is the strongest expression of the insight, not novelty for its own sake.

**Bad:** Copy-pasting main's "Lock the CONFIG_DB cache before modifying VRF" into a
document where NodeActor serialization replaced manual locking.

**Also bad:** Rewriting a sharp, concrete warning ("if you skip the save, a reboot
erases everything since last save — silently") into bland passive voice ("unsaved
changes may be lost upon reboot") because the rewriter is composing fresh prose
instead of recognizing that the original already nailed the delivery.

**Good:** The lesson is "concurrent modifications corrupt VRF state." The old text
said it vividly: "two writers to the same VRF table will silently corrupt each
other's routes." Keep that energy, fit the framing: "the server serializes VRF
operations per device; external actors (Ansible, redis-cli) are not coordinated —
two writers to the same VRF table will silently corrupt each other's routes."

The insight survives the architecture change. The best phrasing often does too.

## 28. Mockup Values Must Be Internally Consistent — HOWTO

Guideline #23 says CLI output mockups must match the actual format (columns, status strings). That's necessary but not sufficient. The **values** in mockups — port numbers, PIDs, node counts, table rows — must also be consistent across the entire document.

During the newtlab HOWTO rewrite, the first pass had switch1 at SSH port 13000 in the status mockup but at 13006 in the deploy mockup. Both were individually plausible, but they contradicted each other because they implied different topologies. Port 13000 would be correct if switch1 were the first device alphabetically (no hosts); port 13006 is correct for the 2node-ngdp topology where 6 host devices sort before the switches.

**Rule:** Pick one real topology from the codebase and derive ALL mockups from it. Run the allocation algorithm mentally (or trace the code) to get correct port numbers, node counts, PID relationships, and table row sets. Then verify that every mockup in the document is consistent with the same topology.

Specific things that must be consistent:
- **Port numbers** — trace through `SSHPortBase + i` where `i` is the sorted device index (including hosts that get coalesced)
- **Node counts** — `len(state.Nodes)` includes virtual host entries, not just QEMU processes. The deploy summary saying "(9 nodes)" must match 9 rows in the table.
- **PID relationships** — virtual hosts share the parent VM's PID (a number, not `—`). All virtual host rows must show the same PID as their parent.
- **Table completeness** — if the code iterates all `state.Nodes` without filtering, the mockup must show all nodes, not just the "interesting" ones

The difference between an 8/10 and a 10/10 HOWTO is often this: the 8/10 gets the schema right (correct columns, correct status values), the 10/10 gets the data right (consistent values, complete rows, traceable derivations).

## 29. HOWTOs Need an End-to-End Lifecycle Workflow — HOWTO

Guideline #6 says HLDs and LLDs need end-to-end walkthroughs. HOWTOs need one too, but at a different level — not tracing function calls through the code, but tracing an operator through the full lifecycle of the tool.

A HOWTO with well-structured individual sections (Deploy, Provision, Status, SSH, Destroy) can still fail the reader who asks: "what does a complete session look like?" The individual sections are reference material — correct and complete but disconnected. The end-to-end workflow is where the reader sees the pieces working together.

**Rule:** After the Quick Start (which is the 5-minute path), include an End-to-End Workflow section that:
- Uses a real topology from the codebase (not a hypothetical one)
- Shows accurate output at every step (per #23 and #28)
- Exercises the distinctive features (virtual hosts, data plane testing, status with bridge stats)
- Covers the full lifecycle: create → configure → observe → use → tear down

The Quick Start says "here's the minimum." The End-to-End Workflow says "here's the full experience." Individual sections say "here's the reference for each step." All three serve different readers at different moments.

## 30. Name the Topology Your Mockups Are Based On — HOWTO

When CLI output mockups derive from a real topology (per #28), name it. A mockup that says `✓ Deployed 2node-ngdp (9 nodes)` with no context forces the reader to wonder "why 9?" and "where did these port numbers come from?"

**Rule:** State which topology the example uses and, when relevant, explain why the numbers are what they are. Example: "The 8 virtual hosts share a single QEMU VM (hostvm-0) and thus share its SSH and console ports. Switches get their own ports (indices 6 and 7 in the sorted device list, so `ssh_port_base + 6` and `ssh_port_base + 7`)."

This serves three purposes:
1. The reader can reproduce the example by deploying the named topology
2. Unusual values (like switches not starting at port base + 0) are explained rather than mysterious
3. Future editors know which topology to check when verifying mockup accuracy

## 31. Quick Start Comes Early and Follows Dependency Order — README

A Quick Start section answers "how do I try this?" It must appear early in the document — immediately after "what does this do?" — and its steps must be dependency-ordered: build before run, start server before use CLI, deploy before provision.

When Quick Start appears after multiple conceptual sections (Architecture, Verification Model, Design Philosophy), the reader who wants to try the tool must scroll past content they don't need yet. When steps within Quick Start are unordered (CLI examples that need a running server, but server setup listed third), the reader's first experience is a failure.

**Rule:** Quick Start is the second major section — right after the introductory "what does this do?" material (which may include a "See It Work" showcase). Within Quick Start, every step either has no prerequisites or states them inline. The first thing the reader types after building should be the minimum viable setup, not the most interesting command.

**Dependency ordering applies within steps too.** If the system has a server that must be running before clients work, "start the server" is step 1 (after build), not a footnote under "HTTP API." If VM images must be installed before deploying a lab, that goes before the deploy command, not in a separate "VM images" subsection below it.

**Concrete test:** Read the Quick Start top to bottom and execute each command in order. If any command fails because a prerequisite was described later in the document, the ordering is wrong.

## 32. Diagrams Are Rendered from Source, Never Hand-Drawn — ALL

ASCII diagrams in documentation must be generated from a source file using Graph::Easy, not drawn by hand. Hand-drawn diagrams have misaligned lines, inconsistent box sizes, and break silently when edited.

**Workflow:**

1. Create a DOT source file in `docs/diagrams/` (e.g., `system-overview.dot`)
2. Render with Graph::Easy:
   ```
   PERL5LIB=~/perl5/lib/perl5 ~/perl5/bin/graph-easy --from=dot --boxart < docs/diagrams/system-overview.dot
   ```
3. Paste the rendered output into the markdown document
4. Commit both the `.dot` source and the updated markdown

Install Graph::Easy with `make tools` (works on Linux and macOS — requires Perl, which ships with both).

**Box padding convention:** Control box size with whitespace in DOT node names. `\n` adds vertical padding, spaces add horizontal padding. Use `\n\n` at the end for symmetric bottom padding:

```dot
"\n  Label  \n\n"     // 2-space horizontal padding, 1-line top + bottom padding
"\n  Long Name  \n\n" // same pattern, wider box
```

**Layout control:** Graph::Easy supports DOT attributes for layout:
- `rankdir=TB` (top-to-bottom) or `rankdir=LR` (left-to-right) for overall direction
- `{ rank=same; "A"; "B" }` to force nodes onto the same row
- Edge port hints via Graph::Easy attributes (`start:`, `end:`) when default routing is unclear

**Rules:**
- Every diagram in markdown must have a corresponding `.dot` source in `docs/diagrams/`
- Never hand-edit rendered output — if the layout is wrong, fix the `.dot` source and re-render
- Words in boxes and on edge labels must have at least 3 characters of clearance from the nearest box edge

## 33. Metaphors Must Be Domain-Accurate, Not Just Evocative — HLD+LLD

A metaphor that sounds good but builds the wrong mental model is worse than no metaphor at all — the reader walks away confident in an understanding that will mislead them.

The test: does the metaphor help the reader *reason* about the actual system, or does it just make the prose more interesting? If the analogy breaks down the moment the reader pushes on it, it weakens rather than strengthens.

**Bad:** "newtron's validation layer is the compiler." A compiler *transforms* source code into executable output. Validation doesn't transform anything — it *rejects* invalid input. The reader who internalizes "compiler" will expect transformation semantics that don't exist.

**Bad:** "In programming, the function is the atomic unit of computation. In networking, the interface is the atomic unit of service." The parallel structure is pleasing, but functions compose (you call one from another); interfaces are attachment points (you bind services to them). The analogy is shallow — it matches on "atomic unit" but diverges on every other axis.

**Good:** "Terraform owns its state file. Kubernetes owns its etcd. They can be reconcilers because they are the sole writer." This is contrast, not analogy — it establishes what newtron is *not* by showing systems that occupy a different position. The reader's existing knowledge of Terraform/K8s builds the correct mental model of why newtron's approach differs.

**Good:** "CONFIG_DB is a flat key-value store, but its consumers are not." This reveals hidden structure — the reader already knows Redis is flat, and the sentence reframes their understanding by pointing at the daemons that impose invisible structure on top.

Guideline #2 (precision before readability) covers factual accuracy. This guideline covers *analogical* accuracy — ensuring that metaphors, analogies, and cross-domain comparisons build the right mental model, not just an appealing one.

## 34. Lead with Universal Truths, Not Feature Descriptions — HLD

The strongest openings in design documents state something true *beyond* the system being described — a principle from the domain itself — before showing how the system embodies it. This draws the reader in by connecting to their existing experience rather than asking them to learn a new system's vocabulary first.

**Flat:** "newtron validates every CONFIG_DB entry against a YANG-derived schema before writing it to Redis."

**Illuminating:** "CONFIG_DB is a database without a schema. Redis accepts anything — misspelled field names, out-of-range values, entries that reference tables that don't exist. Nothing rejects the write. The daemons downstream discover the problem — minutes later, as a silent failure, a crash, or an unrecoverable state."

The first version describes a feature. The second describes the *problem in the domain* that makes the feature necessary. A reader who has never used newtron learns something from the second version — about Redis, about SONiC's architecture, about the failure mode that any CONFIG_DB tool must handle. The feature description follows naturally once the problem is felt.

This applies primarily to design documents and architecture sections. API references and HOWTOs should lead with what the reader needs to do, not why it matters philosophically — the design document already made that case.

## 35. Write from Conviction, Not Summary — HLD

Design prose has two voices: the voice of someone who *built* the system and is sharing hard-won lessons, and the voice of someone *reporting* on what was built. The first earns trust; the second reads like documentation.

**Summary voice:** "We learned that reverse operations are important for preventing orphaned CONFIG_DB entries."

**Conviction voice:** "A configuration database without reverse operations only accumulates. State grows monotonically. Given enough operations over enough time, the device becomes unknowable — crusted with orphaned entries that no one remembers creating and no tool knows how to remove."

The summary tells the reader *that* something matters. The conviction shows them *why* — through concrete consequences that make the principle feel inevitable rather than chosen.

This is distinct from guideline #27 (carry forward substance, not text), which is about preserving insight during rewrites. This guideline is about *generating* the conviction in the first place — writing as someone who has lived through the failure mode, not someone cataloguing the system's properties.

Signs of summary voice that should be revised:
- "We found that..." / "We learned that..." / "It turned out that..."
- Leading with the solution rather than the problem it solves
- Describing what the system does without explaining why the alternative fails
- Passive constructions that distance the writer from the claim ("it was decided that...")
