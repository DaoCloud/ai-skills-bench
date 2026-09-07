#!/usr/bin/env bash
set -euo pipefail

TASK_OUTPUT_DIR="${K8S_AI_BENCH_TASK_OUTPUT_DIR:?K8S_AI_BENCH_TASK_OUTPUT_DIR is required}"
AUDIT_PATH="${K8S_AI_BENCH_CLI_AUDIT:-${TASK_OUTPUT_DIR}/cli-audit.jsonl}"
LOG_PATH="${TASK_OUTPUT_DIR}/log.txt"

test -f "${AUDIT_PATH}"
test -f "${LOG_PATH}"

python3 - "${AUDIT_PATH}" <<'PY'
import json
import sys

audit_path = sys.argv[1]
calls = []
with open(audit_path, encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        calls.append(json.loads(line))

if not calls:
    raise SystemExit("no CLI calls were audited")

dce_calls = [call for call in calls if call.get("name") == "dce"]
if not dce_calls:
    raise SystemExit("dce CLI was not called")

def has_args(*expected):
    for call in dce_calls:
        argv = call.get("argv") or []
        if all(item in argv for item in expected):
            return True
    return False

if not has_args("container-management", "cluster", "get-cluster"):
    raise SystemExit("dce get-cluster was not called")

if not (
    has_args("container-management", "core", "list-nodes")
    or has_args("container-management", "core", "list-pods")
    or has_args("container-management", "core", "list-cluster-events")
):
    raise SystemExit("dce did not perform follow-up cluster inspection")
PY

grep -qi 'kpanda-global-cluster' "${LOG_PATH}"
grep -Eq 'Cluster Health Diagnosis Report|Cluster.*Diagnosis|Health.*Diagnosis' "${LOG_PATH}"
grep -Eq 'Root-Cause|Root Cause|Recommended Actions|Recommended' "${LOG_PATH}"
