#!/usr/bin/env bash
set -euo pipefail

: "${DCE_HOST:?DCE_HOST is required}"
: "${DCE_TOKEN:?DCE_TOKEN is required}"

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

python3 - "${script_dir}/prompt.template" "${script_dir}/prompt.rendered" <<'PY'
import os
import sys
from pathlib import Path

source = Path(sys.argv[1])
target = Path(sys.argv[2])
content = source.read_text(encoding="utf-8")
for name in ("DCE_HOST", "DCE_TOKEN"):
    content = content.replace("${" + name + "}", os.environ[name])
target.write_text(content, encoding="utf-8")
PY
