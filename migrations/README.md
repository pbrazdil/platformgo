# Migrations

Forward migrations are named:

```text
YYYYMMDDHHMMSS_description.up.sql
```

A migration's path and bytes become immutable after merge to a protected branch
or application to a shared or persistent database, whichever occurs first.
Application only to an explicitly disposable local/test database does not
freeze an unpublished, unshared candidate; reset that database before applying
changed bytes. Before freeze the candidate may be edited, renamed, reordered,
deleted, amended, or squashed. After freeze, schema corrections are new forward
migration files. Production down migrations are not used.

See `DATABASE.md`.
