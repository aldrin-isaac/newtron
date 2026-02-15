# Newtron Project — Claude Code Instructions

## Project Documentation

Read these before making design decisions or writing code in unfamiliar areas:

| Document | Path | Contents |
|----------|------|----------|
| newtron HLD | `docs/newtron/hld.md` | Architecture, verification primitives, Redis interaction model |
| newtron LLD | `docs/newtron/lld.md` | Type definitions, method signatures, package structure |
| Device LLD | `docs/newtron/device-lld.md` | CONFIG_DB/APP_DB/ASIC_DB/STATE_DB layer, SSH tunneling, ChangeSets |
| newtron HOWTO | `docs/newtron/howto.md` | Operational patterns, provisioning flow |
| newtest HLD | `docs/newtest/hld.md` | E2E test framework design |
| newtest LLD | `docs/newtest/lld.md` | Step actions, suite mode, dependency ordering |
| newtest HOWTO | `docs/newtest/howto.md` | Writing scenarios, running suites |
| newtlab HLD | `docs/newtlab/hld.md` | VM orchestration, QEMU, bridge networking |
| newtlab LLD | `docs/newtlab/lld.md` | Deploy phases, state persistence, multi-host |
| newtlab HOWTO | `docs/newtlab/howto.md` | Deploying topologies, troubleshooting |
| RCA index | `docs/rca/` | 18 root-cause analyses — SONiC pitfalls and workarounds |

When encountering a SONiC-specific issue (config reload, frrcfgd, orchagent, VPP), check `docs/rca/` first — there are 18 documented pitfalls with root causes and solutions.

## Redis-First Interaction Principle

newtron is a Redis-centric system. All device interaction MUST go through SONiC Redis databases (CONFIG_DB, APP_DB, ASIC_DB, STATE_DB). See `docs/newtron/hld.md` for the full interaction model.

When Redis does not expose the required data or operation, CLI/SSH commands may be used **only as documented workarounds**. Every such call site MUST be tagged:

```go
// CLI-WORKAROUND(id): <what this does>.
// Gap: <what Redis/SONiC lacks>.
// Resolution: <what upstream change would eliminate this>.
```

- **Workaround** — Redis could provide this but doesn't today. Tag with `CLI-WORKAROUND`.
- **Inherent** — Will always require CLI (e.g., `config save`, `docker restart`, filesystem reads). No tag needed, but add a brief comment explaining why CLI is required.

Before adding any `session.Run()`, `ExecCommand()`, or shell command construction in `pkg/newtron/device/` or `pkg/newtron/network/`:

1. Check if the data is available in CONFIG_DB, APP_DB, ASIC_DB, or STATE_DB
2. If it is, use the Redis path
3. If it isn't, add the `CLI-WORKAROUND` tag with a resolution path
4. Never normalize CLI calls — they are exceptions, not the standard interaction model

## Allowed Commands

These are routine project commands that do not require confirmation:

### Go Toolchain
- `go build -o bin/<tool> ./cmd/<tool>`
- `go test ./... -count=1` (and per-package variants)
- `go vet ./...`
- `go run`, `go mod tidy`, `go get`, `go list`, `go doc`, `go version`

### Git
- `git status`, `git diff`, `git log`, `git add`, `git commit`, `git push`
- `git mv`, `git rm`, `git format-patch`, `git reset`, `git am`

### Project Binaries
- `bin/newtlab`, `bin/newtron`, `bin/newtest`, `bin/newtlink` (all subcommands)

### Make
- `make build`, `make test`, `make lint`

### Misc
- `ls`, `stat`, `file`, `wc`, `chmod`, `ln`
- `pgrep`, `pkill`, `ps`
- `ssh`, `sshpass`, `ssh-keygen`, `nc`, `socat`, `curl`, `ping`
- `qemu-img info`, `qemu-img convert`

### Web Access
- `WebSearch` (always allowed)
- `WebFetch` for: `github.com`, `raw.githubusercontent.com`, `containerlab.dev`, `hackmd.io`, `sonic.software`, `deepwiki.com`, `r12f.com`

## Build Convention

Always `go build -o bin/<tool> ./cmd/<tool>` before testing — `go run` compiles to a temp dir and breaks sibling binary resolution.

## Static Analysis

golangci-lint is not installed. Use `go vet` for static analysis.

## Model Routing

Use the primary model (Opus) for:
- Architectural decisions, audits, and planning
- Determining what to change and why
- Code review and correctness reasoning

Dispatch subagents with `model: "sonnet"` for:
- Applying known edits across files (renames, import path updates, deletions)
- Running build/test/commit cycles
- Grep/read research tasks with clear search criteria
- Doc updates where the changes are already specified

## User Preferences

- Never compact away the last 5 prompts and responses during context compression.
