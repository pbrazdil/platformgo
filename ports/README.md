# Test port ledger

`test-port-map.csv` is the authoritative ledger for the clean-room test port.
It contains one row per source test function.

The pinned revisions are recorded in `source-revisions.env`. Source checkouts
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

