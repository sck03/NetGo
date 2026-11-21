#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm ci --no-fund
npm run build
npm test
go test ./internal/...
go vet ./internal/...
mkdir -p dist/linux-amd64
for name in netgo-server netgo-helper netgoctl netgo-client-service netgo-ddns; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "dist/linux-amd64/$name" "./cmd/$name"
done
(cd dist && sha256sum linux-amd64/* > SHA256SUMS)
