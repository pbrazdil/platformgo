# Test port ledger

`test-port-map.csv` is the authoritative ledger for the clean-room test port.
It contains one row per source test function.

The pinned revisions, source roots, and expected inventory counts are recorded
only in `SOURCE_REVISIONS.md`. Source checkouts
used for inventory and static reading live under the ignored `.sources/`
directory. They must never be executed or used as a differential oracle.

Regenerate the initial discovered inventory with:

```bash
go run ./tools/testinventory \
  -platform .sources/platform \
  -nautilus .sources/nautilus_trader \
  -out ports/test-port-map.csv
```

The generator refuses to overwrite a ledger containing work beyond the
`discovered` state. Once porting begins, update the ledger intentionally.

The ledger separates mechanical porting, semantic review, production wiring,
evidence, milestone, port ownership, and implementation ownership. The exact
columns, allowed values, and transitions are defined in `TESTING.md` and
enforced by `scripts/check-port-map.py`. Provenance is parsed from the
documentation attached to the exact mapped Go test function.
