#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

make policy
make fmt-check
make lint
make test
make test-race
make test-repeat
make vuln
