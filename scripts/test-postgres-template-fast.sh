#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PG_BIN="${PLATFORMGO_TEST_POSTGRES_BIN:-/opt/homebrew/opt/postgresql@19/bin}"
if [[ ! -x "$PG_BIN/postgres" || ! -x "$PG_BIN/initdb" || ! -x "$PG_BIN/pg_ctl" ]]; then
  echo "PostgreSQL 19 test binaries not found in $PG_BIN" >&2
  echo "Set PLATFORMGO_TEST_POSTGRES_BIN to the directory containing postgres and initdb." >&2
  exit 1
fi
if [[ "$($PG_BIN/postgres --version)" != "postgres (PostgreSQL) 19beta2"* ]]; then
  echo "Fast template tests require PostgreSQL 19beta2 exactly." >&2
  exit 1
fi

TEMP_ROOT="${TMPDIR:-/tmp}"
PRIMARY_DATA="$(mktemp -d "$TEMP_ROOT/platformgo-pg-primary.XXXXXX")"
TEMPLATE_DATA="$(mktemp -d "$TEMP_ROOT/platformgo-pg-template.XXXXXX")"
PRIMARY_SOCKET="$(mktemp -d "$TEMP_ROOT/platformgo-pg-primary-socket.XXXXXX")"
TEMPLATE_SOCKET="$(mktemp -d "$TEMP_ROOT/platformgo-pg-template-socket.XXXXXX")"

remove_temp_dir() {
  local path="$1"
  case "$path" in
    "$TEMP_ROOT"/platformgo-pg-*) ;;
    *)
      echo "Refusing to remove unexpected temporary path: $path" >&2
      return 1
      ;;
  esac
  if command -v trash >/dev/null 2>&1; then
    trash "$path"
  else
    find "$path" -depth -delete
  fi
}

cleanup() {
	local command_status=$?
	local cleanup_status=0
	trap - EXIT
	set +e
	for data in "$PRIMARY_DATA" "$TEMPLATE_DATA"; do
		if "$PG_BIN/pg_ctl" -D "$data" status >/dev/null 2>&1; then
			if ! "$PG_BIN/pg_ctl" -D "$data" -m fast -w stop >/dev/null 2>&1; then
				echo "Failed to stop temporary PostgreSQL cluster: $data" >&2
				cleanup_status=1
			fi
		fi
	done
	for path in "$PRIMARY_DATA" "$TEMPLATE_DATA" "$PRIMARY_SOCKET" "$TEMPLATE_SOCKET"; do
		if ! remove_temp_dir "$path"; then
			echo "Failed to remove temporary PostgreSQL path: $path" >&2
			cleanup_status=1
		fi
	done
	if ((command_status != 0)); then
		exit "$command_status"
	fi
	exit "$cleanup_status"
}
trap cleanup EXIT

"$PG_BIN/initdb" -D "$PRIMARY_DATA" -A trust -U postgres --locale=C --no-sync >/dev/null
"$PG_BIN/initdb" -D "$TEMPLATE_DATA" -A trust -U postgres --locale=C --no-sync >/dev/null
"$PG_BIN/pg_ctl" -D "$PRIMARY_DATA" -o "-k '$PRIMARY_SOCKET' -h ''" -w start >/dev/null
"$PG_BIN/pg_ctl" -D "$TEMPLATE_DATA" -o "-k '$TEMPLATE_SOCKET' -h ''" -w start >/dev/null
"$PG_BIN/createdb" -h "$PRIMARY_SOCKET" -U postgres platformgo_test
"$PG_BIN/createdb" -h "$TEMPLATE_SOCKET" -U postgres platformgo_template_root

export PLATFORMGO_TEST_POSTGRES_DSN="postgres://postgres@/platformgo_test?host=$PRIMARY_SOCKET&sslmode=disable"
export PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN="postgres://postgres@/platformgo_template_root?host=$TEMPLATE_SOCKET&sslmode=disable"
export PLATFORMGO_TEST_POSTGRES_RESET_AUTHORIZED="YES_I_UNDERSTAND_THIS_DROPS_SCHEMAS"
export PLATFORMGO_TEST_POSTGRES_TEMPLATE_AUTHORIZED="YES_I_UNDERSTAND_THIS_CREATES_AND_DROPS_DATABASES"

durability="$($PG_BIN/psql "$PLATFORMGO_TEST_POSTGRES_DSN" -Atc \
  "SELECT current_setting('fsync'), current_setting('synchronous_commit'), current_setting('full_page_writes'), current_setting('wal_level')")"
if [[ "$durability" != "on|on|on|replica" ]]; then
  echo "Canonical PostgreSQL durability settings are unsafe: $durability" >&2
  exit 1
fi

./scripts/test-postgres-template.sh
