# Secret Store — Operational HOWTO

The secret store (auth-design.md L0) holds plaintext secret values
referenced from spec files. Spec values may contain
`${secret:KEY}` references; when newtron-server is started with
`--secret-store=PATH`, each reference is resolved against the JSON
map at PATH at network load time. The plaintext stays in the
operator's secret file (mode 0600), not in the version-controlled
spec directory.

This doc covers the operator's day-to-day workflow: putting,
listing, and rotating secrets; configuring servers; what happens
when references are missing or the store is unreadable.

## 1. What gets resolved

Two spec fields currently carry `${secret:KEY}` references:

| Spec | Field | File location |
|---|---|---|
| `DeviceProfile.SSHPass` | `ssh_pass` | `profiles/<device>.json` |
| `DeviceProfile.SSHUser` | `ssh_user` | `profiles/<device>.json` |
| `VMCredentials.Pass` | `vm_credentials.pass` | `platforms.json` (per platform) |
| `VMCredentials.User` | `vm_credentials.user` | `platforms.json` (per platform) |

Other spec fields pass through unchanged. A future Store implementation
may add encryption-at-rest of the secret file itself — the resolver
contract doesn't change.

## 2. Configure the server

Two flags, both default empty (current behavior preserved):

- `newtron-server --secret-store=PATH`
- `newt-server --secret-store=PATH` (forwarded to the composed
  newtron engine)

The store file is created if it doesn't exist; if it already exists,
mode broader than 0600 (group- or world-readable) is rejected at
startup so a misconfigured permissions setup fails loud rather than
serving secrets under wrong perms.

Typical operator setup:

```sh
mkdir -m 700 -p ~/.newtron
bin/newtron-server --listen 127.0.0.1:19080 \
                   --spec-dir /etc/newtron/lab \
                   --secret-store ~/.newtron/secrets.json
```

When `--secret-store` is set but a referenced KEY is missing, the
server fails to load that network with a clear error naming the
missing key. The operator runs `bin/newtron secrets put KEY VALUE`
and either restarts the server or calls ReloadNetwork on each
affected network.

## 3. Manage the store

The `bin/newtron secrets` subcommand operates directly on the
operator's store file. It does not contact newtron-server — secret
management is a local-filesystem operation.

### Put a secret

```sh
# Inline (visible in shell history):
bin/newtron secrets --store ~/.newtron/secrets.json put switch1-ssh YourPaSsWoRd

# From stdin (recommended for high-sensitivity values):
echo -n "$NEWPASSWORD" | bin/newtron secrets --store ~/.newtron/secrets.json put switch1-ssh -
```

### Get / list / delete

```sh
bin/newtron secrets --store ~/.newtron/secrets.json get switch1-ssh    # prints to stdout, no newline
bin/newtron secrets --store ~/.newtron/secrets.json list                # prints keys, one per line
bin/newtron secrets --store ~/.newtron/secrets.json delete switch1-ssh
```

`delete` on a non-existent key prints `<key> (was not set)` and
returns success — idempotent so cleanup scripts can run repeatedly.

## 4. Reference secrets from spec files

Edit `profiles/<device>.json` or `platforms.json` to swap the
plaintext value for a reference:

```diff
 {
   "mgmt_ip": "10.0.0.1",
   "ssh_user": "admin",
-  "ssh_pass": "YourPaSsWoRd",
+  "ssh_pass": "${secret:switch1-ssh}",
   "underlay_asn": 65001
 }
```

The literal string `${secret:switch1-ssh}` is the reference; newtron
strips the `${secret:` prefix and `}` suffix and looks up
`switch1-ssh` in the store.

Plaintext values keep working — operators migrate incrementally. A
mixed profile (some plaintext, some references) is fine; the loader
resolves each value independently.

## 5. Rotation and reload

The server reads the store file once when it loads each network.
Editing the store file does NOT auto-trigger a reload — the server
is intentionally not watching the file (avoids the complexity of
detecting partial writes, surprise rotation, etc.).

After changing a stored value:

```sh
bin/newtron secrets --store ~/.newtron/secrets.json put switch1-ssh NEWPASSWORD
curl -X POST http://127.0.0.1:19080/newtron/v1/networks/default/reload
```

The reload re-resolves references against the new store content.
Networks not touched by the reload keep their previously-resolved
values until they're individually reloaded (or the server restarts).

## 6. Threat model — what L0 addresses, what it doesn't

**Addressed**:

- *Plaintext passwords in the version-controlled spec directory*.
  The 58 plaintext password instances across shipped topologies were
  the original L0 motivator; after migration, `grep -r ssh_pass
  newtrun/topologies/` finds only references.
- *Misconfigured permissions*. `NewFileStore` refuses to open a
  store file with mode broader than 0600. An operator who
  accidentally `chmod 644`s the file gets a startup error instead of
  silently exposing secrets.
- *Atomic write*. Set/Delete go through tmp+rename so a crash during
  write leaves either the old file or the new file in place — never
  a partial JSON object.

**Not addressed in L0** (separate concerns, separate layers):

- *Encryption at rest of the secret file itself*. The shipped
  FileStore writes plaintext. An attacker who can read the file
  (e.g., via backup access, a host compromise) gets the values. A
  future Store implementation (age-encrypted, KMS-backed) plugs into
  the same interface; operators choose at deployment time.
- *Server-side key rotation tracking*. L0 ships the manual
  rotate-and-reload flow above. L6 (auth-design.md) will add
  automatic spec-file watching so rotations propagate without
  explicit reload.
- *Per-secret authorization*. Anyone with access to the secret store
  file or the operator's CLI can read every secret. Per-secret
  grants (`alice can read switch1-ssh but not switch2-ssh`) are out
  of scope; the operator's existing file-system permissions control
  who reads the store.

## 7. Cross-references

- [`auth-design.md`](auth-design.md) — L0 in the layered auth plan
- [`hld.md`](hld.md) §9 — operator-facing security framing
- `pkg/newtron/secret/` — the Store interface and FileStore
  implementation
- `cmd/newtron/cmd_secrets.go` — operator CLI source
