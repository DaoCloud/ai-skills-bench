# k8s-ai-hermes-bridge

`k8s-ai-hermes-bridge` is a minimal compatibility bridge from `k8s-ai-bench --agent-bin` to the token-factory-copilot Hermes Agent API Server.

It does not load skills, install CLI tools, execute shell commands, route to the real LLM, or bridge the benchmark kubeconfig. Those responsibilities belong to the token-factory-copilot Helm chart and the Hermes Agent running behind `:8642`.

## Environment

```bash
export COPILOT_HERMES_BASE_URL=http://127.0.0.1:8642/v1
export COPILOT_HERMES_API_KEY=<API_SERVER_KEY>
export COPILOT_HERMES_MODEL=hermes-agent
export COPILOT_HERMES_TIMEOUT=1800s
```

`COPILOT_HERMES_BASE_URL` may be the service root, the API base URL, or the full chat completions endpoint:

```bash
export COPILOT_HERMES_BASE_URL=http://127.0.0.1:8642
export COPILOT_HERMES_BASE_URL=http://127.0.0.1:8642/v1
export COPILOT_HERMES_BASE_URL=http://127.0.0.1:8642/v1/chat/completions
```

By default, `--model` is recorded as benchmark metadata only. Set this when Hermes supports request-level model routing:

```bash
export COPILOT_HERMES_USE_BENCH_MODEL=true
```

## Build

```bash
go test ./cmd/k8s-ai-hermes-bridge
go build -o k8s-ai-hermes-bridge ./cmd/k8s-ai-hermes-bridge
```

## Direct Run

```bash
printf 'Confirm the Copilot skills are loaded.\n' | ./k8s-ai-hermes-bridge \
  --kubeconfig /tmp/kubeconfig.yaml \
  --llm-provider openai \
  --model qwen/qwen3-coder \
  --trace-path .build/tfc-bench/smoke/trace.json \
  --quiet=true \
  --enable-tool-use-shim=false \
  --skip-permissions \
  --show-tool-output \
  --mcp-client=false
```

## Matrix Agent

Use `generic-stdin` when wiring this bridge into matrix mode. The benchmark writes the final prompt to stdin and passes provider/model metadata with `--llm-provider` and `--model`.

```yaml
agents:
  - id: hermes
    bin: ./k8s-ai-hermes-bridge
    adapter: generic-stdin

models:
  - id: qwen3-coder
    provider: openai
    model: Qwen/Qwen3-Coder-480B-A35B-Instruct
```

`kubectl-ai`-style flags such as `--trace-path` and `--kubeconfig` remain supported for compatibility, but they are not required by the recommended matrix path.

## Request Details

The bridge sends an OpenAI-compatible Chat Completions request to Hermes:

```bash
curl -X POST http://127.0.0.1:8642/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $COPILOT_HERMES_API_KEY" \
  -d '{
    "model": "hermes-agent",
    "temperature": 0,
    "messages": [
      {
        "role": "system",
        "content": "You are running token-factory-copilot e2e benchmark.\nSkills and CLI tools are already loaded by Hermes Agent through the token-factory-copilot Helm chart.\nUse the server-side Hermes Agent environment. Do not assume the runner process can provide local skills, local CLI tools, or local kubeconfig files.\nBenchmark metadata:\n- provider: <provider>\n- benchmark_model: <model>\n- kubeconfig: <kubeconfig> (metadata only; the file is not bridged into Hermes)"
      },
      {
        "role": "user",
        "content": "<stdin lines joined by newline>"
      }
    ]
  }'
```

- **model**: defaults to `hermes-agent`; overridden by `--model` when `COPILOT_HERMES_USE_BENCH_MODEL=true`
- **messages**: system prompt is a fixed template; user message is stdin lines joined by `\n\n`
- **temperature**: hardcoded to `0`

## Helm Port Forward

```bash
kubectl -n tokenfactory-system port-forward svc/tokenfactory-copilot-hermes-agent 8642:8642
```

## Live Hermes Test

The live integration test is disabled by default. It uses the current environment and never stores `COPILOT_HERMES_API_KEY` in source or trace.

```bash
export COPILOT_HERMES_BASE_URL=http://<hermes-host>:<port>/v1/chat/completions
export COPILOT_HERMES_API_KEY=<API_SERVER_KEY>
export COPILOT_HERMES_MODEL=public/minimax-m25
export COPILOT_HERMES_LIVE_TEST=1

go test -run TestLiveHermesEnvironment -count=1
```
