# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.6
ARG BUN_VERSION=1.3.4
ARG GOPROXY=https://proxy.golang.org,direct
ARG VERSION=codespaces
ARG BUILD_TIME=unknown

FROM oven/bun:${BUN_VERSION} AS web-builder

WORKDIR /src/internal/web

COPY internal/web/package.json internal/web/bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache \
    bun install --frozen-lockfile

COPY internal/web/ ./
COPY assets/ /src/assets/
RUN bun run build

FROM golang:${GO_VERSION}-bookworm AS server-builder

ARG GOPROXY
ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . ./
ARG VERSION
ARG BUILD_TIME
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -buildvcs=false -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /out/weaveflow-server ./cmd/server

FROM alpine:3.24

ARG VERSION

LABEL org.opencontainers.image.title="WeaveFlow Codespaces Demo" \
      org.opencontainers.image.description="Disposable Workbench demo for WeaveFlow" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/dengzii/weaveflow"

RUN apk add --no-cache \
      bash \
      ca-certificates \
      curl \
      gettext-envsubst \
      git \
      gcompat \
      libgcc \
      libstdc++ \
      nginx \
      openssh-client \
    && adduser -D -s /bin/bash codespace \
    && mkdir -p /app/demos /workspace /tmp/weaveflow /tmp/weaveflow-codespaces/server /tmp/weaveflow-codespaces/secrets /tmp/nginx-client-body /tmp/nginx-proxy-temp /tmp/nginx-fastcgi-temp /tmp/nginx-uwsgi-temp /tmp/nginx-scgi-temp \
    && chown -R codespace:codespace /app /workspace /tmp/weaveflow /tmp/weaveflow-codespaces /tmp/nginx-client-body /tmp/nginx-proxy-temp /tmp/nginx-fastcgi-temp /tmp/nginx-uwsgi-temp /tmp/nginx-scgi-temp

WORKDIR /app

COPY --from=server-builder --chown=codespace:codespace /out/weaveflow-server /app/weaveflow-server
COPY --from=web-builder --chown=codespace:codespace /src/internal/web/dist/ /usr/share/nginx/html/
COPY --chown=codespace:codespace examples/codespaces_demo/graph.json /app/demos/codespaces-demo.json
COPY --chown=codespace:codespace .devcontainer/demo/docker-entrypoint.sh /usr/local/bin/codespaces-entrypoint.sh
COPY --chown=codespace:codespace scripts/nginx.conf.template /etc/weaveflow/nginx.conf.template
COPY --chown=codespace:codespace scripts/web-config.js.template /etc/weaveflow/web-config.js.template

RUN chmod 0755 /usr/local/bin/codespaces-entrypoint.sh

ENV HOME=/home/codespace \
    GIN_MODE=release \
    WEAVEFLOW_WEB_PORT=8080 \
    WEAVEFLOW_SERVER_PORT=8081 \
    WEAVEFLOW_DATA_DIR=/tmp/weaveflow-codespaces/server \
    WEAVEFLOW_SECRET_DIR=/tmp/weaveflow-codespaces/secrets \
    WEAVEFLOW_LOG_LEVEL=info \
    WEAVEFLOW_TOOL_WORKDIR=/workspace \
    WEAVEFLOW_CODEX_WORKSPACE_ROOTS=/workspace \
    WEAVEFLOW_CLAUDE_WORKSPACE_ROOTS=/workspace \
    WEAVEFLOW_BOOTSTRAP_GRAPH=/app/demos/codespaces-demo.json \
    WEAVEFLOW_BOOTSTRAP_GRAPH_ID=codespaces_demo \
    WEAVEFLOW_BOOTSTRAP_GRAPH_VERSION=1.0

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --start-interval=1s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:$WEAVEFLOW_WEB_PORT/healthz" || exit 1

USER codespace
ENTRYPOINT ["/usr/local/bin/codespaces-entrypoint.sh"]
