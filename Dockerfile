FROM golang:1.24-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/k8s-ai-bench . \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/generic-llm-agent ./cmd/generic-llm-agent \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/k8s-ai-hermes-bridge ./cmd/k8s-ai-hermes-bridge

FROM debian:bookworm-slim

ARG TARGETARCH
ARG KIND_VERSION=0.23.0
ARG KUBECTL_VERSION=1.30.0
ARG VCLUSTER_VERSION=0.20.0

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        docker.io \
        git \
        python3 \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    arch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    curl -fsSL -o /usr/local/bin/kubectl "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${arch}/kubectl"; \
    chmod +x /usr/local/bin/kubectl; \
    curl -fsSL -o /usr/local/bin/kind "https://github.com/kubernetes-sigs/kind/releases/download/v${KIND_VERSION}/kind-linux-${arch}"; \
    chmod +x /usr/local/bin/kind; \
    curl -fsSL -o /usr/local/bin/vcluster "https://github.com/loft-sh/vcluster/releases/download/v${VCLUSTER_VERSION}/vcluster-linux-${arch}"; \
    chmod +x /usr/local/bin/vcluster

COPY --from=builder /out/k8s-ai-bench /usr/local/bin/k8s-ai-bench
COPY --from=builder /out/generic-llm-agent /usr/local/bin/generic-llm-agent
COPY --from=builder /out/k8s-ai-hermes-bridge /usr/local/bin/k8s-ai-hermes-bridge

WORKDIR /bench
COPY tasks ./tasks
COPY skills ./skills
COPY clis ./clis
COPY site ./site
COPY eval-matrix.yaml eval-matrix-hermes.yaml ./
RUN mkdir -p .build

ENTRYPOINT ["k8s-ai-bench"]
CMD ["--help"]
