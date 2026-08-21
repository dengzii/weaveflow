ARG GO_VERSION=1.26.6
ARG BUN_VERSION=1.3.4
ARG GOPROXY=https://goproxy.cn,direct
ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_TIME=unknown

FROM oven/bun:${BUN_VERSION} AS web-builder

WORKDIR /src/internal/web

COPY internal/web/package.json internal/web/bun.lock ./
RUN bun install --frozen-lockfile

COPY internal/web/ ./
COPY assets/ /src/assets/
RUN bun run build

FROM golang:${GO_VERSION}-bookworm AS server-builder

ARG GOPROXY
ENV GOPROXY=${GOPROXY}

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
ARG VERSION
ARG BUILD_TIME
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.buildTime=${BUILD_TIME}" \
      -o /out/weaveflow-server ./cmd/server

FROM nginx:alpine

ARG VERSION
ARG VCS_REF

LABEL org.opencontainers.image.title="WeaveFlow" \
      org.opencontainers.image.description="Graph-based runtime for building, executing, and inspecting LLM agents in Go" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.source="https://github.com/dengzii/weaveflow"

RUN apk add --no-cache gettext \
    && mkdir -p /app /data/server /data/secrets /workspace /tmp/weaveflow /tmp/nginx-client-body /tmp/nginx-proxy-temp /tmp/nginx-fastcgi-temp /tmp/nginx-uwsgi-temp /tmp/nginx-scgi-temp \
    && chown -R 101:101 /app /data /workspace /tmp/weaveflow /tmp/nginx-client-body /tmp/nginx-proxy-temp /tmp/nginx-fastcgi-temp /tmp/nginx-uwsgi-temp /tmp/nginx-scgi-temp

WORKDIR /app

COPY --from=server-builder --chown=101:101 /out/weaveflow-server /app/weaveflow-server
COPY --from=web-builder --chown=101:101 /src/internal/web/dist/ /usr/share/nginx/html/
COPY --chown=101:101 scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY --chown=101:101 scripts/nginx.conf.template /etc/weaveflow/nginx.conf.template
COPY --chown=101:101 scripts/web-config.js.template /etc/weaveflow/web-config.js.template

RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh

ENV WEAVEFLOW_WEB_PORT=8080 \
    WEAVEFLOW_SERVER_PORT=8081 \
    WEAVEFLOW_DATA_DIR=/data/server \
    WEAVEFLOW_SECRET_DIR=/data/secrets \
    WEAVEFLOW_LOG_LEVEL=info \
    WEAVEFLOW_TOOL_WORKDIR=/workspace \
    WEAVEFLOW_CODEX_WORKSPACE_ROOTS=/workspace \
    WEAVEFLOW_CLAUDE_WORKSPACE_ROOTS=/workspace

EXPOSE 8080
VOLUME ["/data", "/workspace"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:$WEAVEFLOW_WEB_PORT/healthz" || exit 1

USER 101
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
