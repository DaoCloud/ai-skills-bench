#!/usr/bin/env bash
set -euo pipefail

TASK_OUTPUT_DIR="${K8S_AI_BENCH_TASK_OUTPUT_DIR:?K8S_AI_BENCH_TASK_OUTPUT_DIR is required}"
LOG_PATH="${TASK_OUTPUT_DIR}/log.txt"

test -f "${LOG_PATH}"

require_log_pattern() {
  local pattern="$1"
  local description="$2"
  if ! grep -Eqi "$pattern" "${LOG_PATH}"; then
    echo "missing expected ${description}: ${pattern}" >&2
    exit 1
  fi
}

require_log_pattern 'kpanda-global-cluster' 'cluster name'
require_log_pattern 'cluster|health|diagnosis|诊断|健康' 'diagnosis wording'
require_log_pattern 'node|节点' 'node summary'
require_log_pattern 'pod|pods|工作负载' 'pod inspection'
require_log_pattern 'root.?cause|recommended|recommendation|action|根因|建议|处理' 'root cause or recommendation'
