# Running with Docker

The Docker image contains `k8s-ai-bench`, the bundled `generic-llm-agent` and
`k8s-ai-hermes-bridge` binaries, and the common Kubernetes CLIs used by the
benchmark (`kubectl`, `kind`, `vcluster`, and Docker CLI).

Build the image from the repository root:

```sh
docker build -t k8s-ai-bench .
```

Images built from `main` and release tags are also published to GHCR:

```sh
docker pull ghcr.io/gke-labs/k8s-ai-bench:latest
```

Forks publish branch images to their own GHCR namespace on push, which is useful
for testing PR changes before they land:

```sh
docker pull ghcr.io/<github-user>/k8s-ai-bench:sha-<commit>
```

## Run with kind

The default cluster provider is `kind`. When running in Docker, mount the host
Docker socket so `kind` can create Kubernetes-in-Docker nodes on the host Docker
daemon. Mount your agent binary and an output directory with `-v`.

```sh
mkdir -p .build/k8s-ai-bench

docker run --rm \
  -e GEMINI_API_KEY \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$(command -v kubectl-ai):/usr/local/bin/kubectl-ai:ro" \
  -v "$PWD/.build:/bench/.build" \
  k8s-ai-bench run \
    --agent-bin kubectl-ai \
    --output-dir /bench/.build/k8s-ai-bench \
    --task-pattern fix-probes \
    --concurrency 1
```

Use `--cluster-creation-policy AlwaysCreate` if you want each run to recreate
the shared benchmark kind cluster.

## Run against an existing cluster

To skip cluster creation and run against an existing kubeconfig, mount it into
the container and set `--cluster-creation-policy DoNotCreate`.

```sh
mkdir -p .build/k8s-ai-bench

docker run --rm \
  -e GEMINI_API_KEY \
  -v "$HOME/.kube:/root/.kube:ro" \
  -v "$(command -v kubectl-ai):/usr/local/bin/kubectl-ai:ro" \
  -v "$PWD/.build:/bench/.build" \
  k8s-ai-bench run \
    --agent-bin kubectl-ai \
    --kubeconfig /root/.kube/config \
    --cluster-creation-policy DoNotCreate \
    --output-dir /bench/.build/k8s-ai-bench \
    --task-pattern fix-probes \
    --concurrency 1
```

## Run matrix benchmarks

Matrix files often reference repository-relative paths such as `./tasks`,
`./skills`, and `.build/...`. The image workdir is `/bench`, so the included
matrix files work without rewriting those paths. Mount any local CLIs referenced
by the matrix into `/bench/clis`.

```sh
mkdir -p .build/skill-cli-bench

docker run --rm \
  -e OPENAI_API_BASE \
  -e OPENAI_API_KEY \
  -v "$PWD/clis:/bench/clis:ro" \
  -v "$PWD/.build:/bench/.build" \
  k8s-ai-bench run --matrix-file /bench/eval-matrix.yaml
```

For example, the checked-in `eval-matrix.yaml` expects `./clis/dce` to exist in
the container. If your matrix references local agents that are not already in
the image, mount them into the paths used by the matrix file, or mount a custom
matrix file and point `--matrix-file` at that container path.

## Run with vCluster

For vCluster, mount the host kubeconfig and pass the host context. If the host
cluster is reached through a kubeconfig path other than `--kubeconfig`, also set
`--host-cluster-kubeconfig`.

```sh
mkdir -p .build/k8s-ai-bench

docker run --rm \
  -e GEMINI_API_KEY \
  -v "$HOME/.kube:/root/.kube:ro" \
  -v "$(command -v kubectl-ai):/usr/local/bin/kubectl-ai:ro" \
  -v "$PWD/.build:/bench/.build" \
  k8s-ai-bench run \
    --agent-bin kubectl-ai \
    --cluster-provider vcluster \
    --host-cluster-context <host-context> \
    --kubeconfig /root/.kube/config \
    --output-dir /bench/.build/k8s-ai-bench \
    --task-pattern fix-probes \
    --concurrency 1
```

## Analyze results

The same image can analyze mounted result directories:

```sh
docker run --rm \
  -v "$PWD/.build:/bench/.build" \
  k8s-ai-bench analyze \
    --input-dir /bench/.build/k8s-ai-bench \
    --results-filepath /bench/.build/report.md
```

## Notes

- Output files are written by the container user. The default examples run as
  root because that is the most compatible mode for Docker socket access.
- Environment variables used by your selected LLM provider must be passed with
  `-e`, for example `GEMINI_API_KEY`, `OPENAI_API_KEY`, or `OPENAI_API_BASE`.
- For kind runs, the benchmark talks to the host Docker daemon through
  `/var/run/docker.sock`. This gives the container Docker control over the host.
