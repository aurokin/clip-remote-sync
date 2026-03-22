#!/usr/bin/env bash
set -euo pipefail

repo_root="$({ cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd; })"
cd "$repo_root"

test -f go.mod
test -f cmd/crs/main.go

go list ./cmd/crs >/dev/null
