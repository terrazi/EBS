#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
db_file="$(mktemp -u /tmp/ebs-smoke-XXXXXX.sqlite3)"
port="${EBS_SMOKE_PORT:-18792}"
cd "$root_dir"
python3 -m ebs.server --host 127.0.0.1 --port "$port" --db "$db_file" >/tmp/ebs-smoke-server.log 2>&1 &
server_pid=$!
cleanup() { kill "$server_pid" 2>/dev/null || true; rm -f "$db_file" "$db_file-shm" "$db_file-wal"; }
trap cleanup EXIT
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:${port}/api/health" >/tmp/ebs-health.json 2>/dev/null; then break; fi
  sleep 0.1
done
python3 - <<'PY'
import json
from pathlib import Path
health = json.loads(Path('/tmp/ebs-health.json').read_text())
assert health['status'] == 'ok'
assert health['demo']['customers'] == 30
PY
curl -fsS -X POST "http://127.0.0.1:${port}/api/login" -H 'Content-Type: application/json' -d '{"username":"gise01","password":"1234"}' >/tmp/ebs-login.json
session_token="$(python3 -c 'import json; print(json.load(open("/tmp/ebs-login.json"))["token"])')"
curl -fsS -X POST "http://127.0.0.1:${port}/api/cash/open" -H "Authorization: Bearer ${session_token}" -H 'Content-Type: application/json' -d '{}' >/tmp/ebs-open.json
curl -fsS -X POST "http://127.0.0.1:${port}/api/cash/deposit" -H "Authorization: Bearer ${session_token}" -H 'Content-Type: application/json' -H 'Idempotency-Key: smoke-deposit-1' -d '{"customer_id":1,"account_id":1,"amount":"1.000,00"}' >/tmp/ebs-deposit.json
python3 - <<'PY'
import json
from pathlib import Path
result = json.loads(Path('/tmp/ebs-deposit.json').read_text())
assert result['status'] == 'COMPLETED'
assert result['balanced'] is True
assert result['document']['path'].endswith('.pdf')
PY
echo "EBS smoke test: OK"
