#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$root"

tags=production
if [[ "$(uname -s)" == Linux ]]; then tags=production,webkit2_41; fi

CGO_ENABLED=1 go test -tags "$tags" ./...
CGO_ENABLED=1 go vet -tags "$tags" ./...
go run ./cmd/build "$@"
