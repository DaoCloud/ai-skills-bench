#!/usr/bin/env bash
set -euo pipefail

DCE_HOSTNAME="${DCE_HOSTNAME:-http://10.0.6.152:30448/}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [[ -z "${DCE_TOKEN:-}" && -n "${DCE_TOKEN_FILE:-}" ]]; then
  DCE_TOKEN="$(tr -d '\r\n' < "${DCE_TOKEN_FILE}")"
fi

if [[ -z "${DCE_TOKEN:-}" ]]; then
  echo "DCE_TOKEN or DCE_TOKEN_FILE is required for dce auth login" >&2
  exit 1
fi

printf '%s' "${DCE_TOKEN}" | "${K8S_AI_BENCH_REPO_ROOT:-${REPO_ROOT}}/clis/dce" auth login \
  --hostname "${DCE_HOSTNAME}" \
  --with-token
