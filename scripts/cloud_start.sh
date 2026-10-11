#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
if [[ ! -x var/EBS_v3_Server ]]; then
  echo 'Merkezi Server derlenmemiş; önce scripts/cloud_setup.sh çalıştırın.' >&2
  exit 1
fi
mkdir -p var
export ProgramData="$repo_root/var"
export EBS_V3_LISTEN=0.0.0.0:8792
exec var/EBS_v3_Server
