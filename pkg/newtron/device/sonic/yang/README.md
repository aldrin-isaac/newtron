# SONiC YANG Reference for newtron CONFIG_DB Schema

This directory contains YANG-derived constraint data used to validate
`pkg/newtron/device/sonic/schema.go`. The canonical source is the SONiC
YANG models at:

    https://github.com/sonic-net/sonic-buildimage/tree/master/src/sonic-yang-models/yang-models/

## When to Update

Update this reference and re-audit `schema.go` when:

1. **Upgrading the SONiC buildimage** — re-fetch YANG files, diff against
   `constraints.md`, and update `schema.go` for any changes.
2. **Adding a new CONFIG_DB table** to newtron — fetch the YANG model for
   that table and add constraints to both `constraints.md` and `schema.go`.
3. **A SONiC daemon rejects a CONFIG_DB entry** that passed schema validation —
   the constraint table is missing or wrong; fix it.

## Files

- `constraints.md` — Per-table, per-field constraints extracted from YANG files.
  This is the reference that `schema.go` is validated against.
- `README.md` — This file.
