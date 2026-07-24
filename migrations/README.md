# Migrations

Forward migrations are named:

```text
YYYYMMDDHHMMSS_description.up.sql
```

Once committed/applied, a migration is immutable. Schema corrections are new files. Production down migrations are not used.

See `DATABASE.md`.
