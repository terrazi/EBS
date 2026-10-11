#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_binary=/tmp/go1.23.2/bin/go
go_archive=/tmp/go1.23.2.tar.gz
go_archive_sha=542d3c1705f1c6a1c5a80d5dc62e2e45171af291e755d591c5e6531ef63b454e
go_binary_sha=caa1b3c015e819aaa1408efe37b2499088bbf986bf30a2883bceab4e07b55b52
if ! printf '%s  %s\n' "$go_binary_sha" "$go_binary" | sha256sum --check --status; then
  curl -fL --retry 2 https://go.dev/dl/go1.23.2.linux-amd64.tar.gz -o "$go_archive"
  printf '%s  %s\n' "$go_archive_sha" "$go_archive" | sha256sum --check
  mkdir -p /tmp/go1.23.2
  tar -xzf "$go_archive" -C /tmp/go1.23.2 --strip-components=1
fi
export GOPATH=/tmp/go-path GOCACHE=/tmp/go-build-cache
cd "$repo_root"
python3 -m unittest discover -s tests -v
EBS_SMOKE_PORT=18893 bash scripts/smoke_test.sh
for module in server client installer; do
  (cd "v31/$module" && "$go_binary" test ./... -timeout 180s)
done
mkdir -p var
(cd v31/server && "$go_binary" build -trimpath -o "$repo_root/var/EBS_v3_Server" .)

# Check the actual central Go runtime with isolated, disposable training data.
smoke_data="$(mktemp -d /tmp/ebs-v31-cloud-XXXXXX)"
ProgramData="$smoke_data" EBS_V3_LISTEN=127.0.0.1:18894 var/EBS_v3_Server &
central_pid=$!
cleanup() { kill "$central_pid" 2>/dev/null || true; wait "$central_pid" 2>/dev/null || true; rm -rf "$smoke_data"; }
trap cleanup EXIT
python3 scripts/smoke_v31.py --base-url http://127.0.0.1:18894
