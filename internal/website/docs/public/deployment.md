# Deploy the Debug Server

WeaveFlow can run as a local Go process or as a packaged image that combines the Go server, WebUI, and Nginx. Use the
local process while developing; use the image and Compose for a repeatable deployment.

## Local process

```bash
go build ./cmd/server
go run ./cmd/server \
  -addr 127.0.0.1:8080 \
  -data .local/server \
  -graph examples/codespaces_demo/graph.json
```

This command starts the API only. Run the [Workbench](/guides/workbench) separately when you need the browser UI. The
server exposes `GET /healthz`. Use `-prefix /debug` when a reverse proxy mounts the API below a path prefix. A
non-loopback address requires `WEAVEFLOW_MANAGEMENT_TOKEN`.

## Docker image

Build from the repository root so the Dockerfile can access Go packages and WebUI assets. Replace `0.1.1` with the
version you are deploying:

```bash
docker build --build-arg VERSION=0.1.1 \
  -t weaveflow:0.1.1 -f scripts/Dockerfile .
docker run --detach --init --name weaveflow \
  --restart unless-stopped --read-only \
  --security-opt no-new-privileges:true --cap-drop ALL \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --publish 127.0.0.1:8080:8080 \
  --env-file scripts/.env --volume weaveflow-data:/data \
  --volume "$PWD:/workspace:ro" weaveflow:0.1.1
```

The image runs as a non-root user and generates WebUI configuration under `/tmp`, so a read-only root filesystem is
supported. Keep the workspace read-only unless a workflow explicitly needs file-writing tools.

## Compose

```bash
cp scripts/.env.example scripts/.env
# Set WEAVEFLOW_IMAGE and WEAVEFLOW_MANAGEMENT_TOKEN in scripts/.env.
docker compose --env-file scripts/.env -f scripts/compose.yaml up -d
docker compose --env-file scripts/.env -f scripts/compose.yaml ps
curl http://127.0.0.1:8080/healthz
```

The Compose file uses a prebuilt image. After copying the example file, set `WEAVEFLOW_IMAGE=weaveflow:0.1.1` (or the
tag you built) and replace the example management token. Compose provides a persistent `weaveflow-data` volume, a
loopback-only published port, a health check, and bounded JSON-file logs. Set `WEAVEFLOW_PUBLISH_HOST=0.0.0.0` only
behind a trusted network boundary and always set a strong management token.

## Reverse proxy and TLS

Terminate TLS at a trusted proxy and forward the WebUI port. If the API is mounted below `/debug`, set
`WEAVEFLOW_SERVER_PREFIX=/debug` and configure the Workbench Backend base URL to the same origin and prefix. For a
separate WebUI origin, set `WEAVEFLOW_CORS_ORIGINS` to an explicit allowlist rather than `*`.

## Persistence and backups

The data directory stores Graphs, Sessions, Runs, Checkpoints, Events, Artifacts, Triggers, and managed secrets. Back up
the data volume before upgrades, and test restoring a copy. Pin image tags instead of deploying `latest` when you need
repeatable rollbacks.

## Production boundary

The packaged image hardens process and filesystem defaults, but WeaveFlow does not provide multi-tenant authorization,
quotas, audit policy, or cross-worker takeover. Put those controls, network policy, secret rotation, monitoring, and
backup policy around the server in your deployment environment.

See the repository's [`scripts/README.md`](https://github.com/dengzii/weaveflow/blob/master/scripts/README.md) for all
container variables and the Docker CLI helper.
