# Container deployment

The image contains the Go server and the compiled WebUI behind one non-root Nginx process. Build from the repository
root so the Docker build can access `internal/web`, `assets`, and the Go packages.

## Build and run

Build the image directly:

```bash
docker build --build-arg VERSION=0.1.1 -t weaveflow:0.1.1 -f scripts/Dockerfile .
```

For a local deployment, keep the port bound to loopback:

```bash
docker run --detach --init --name weaveflow \
  --restart unless-stopped \
  --read-only \
  --security-opt no-new-privileges:true \
  --cap-drop ALL \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --publish 127.0.0.1:8080:8080 \
  --env-file .env \
  --volume weaveflow-data:/data \
  --volume "$PWD:/workspace" \
  weaveflow:local
```

If `.env` is not present, remove the `--env-file .env` option. Put model credentials and
`WEAVEFLOW_MANAGEMENT_TOKEN` in that file instead of exposing them in shell history. The `.dockerignore` excludes
`.env` and common key files from the image build context.

The repository also provides a Docker CLI-only helper, which does not require the Compose plugin:

```bash
./scripts/deploy.sh deploy
./scripts/deploy.sh status
./scripts/deploy.sh logs
```

The helper binds `127.0.0.1:8080` by default. Set `WEAVEFLOW_PUBLISH_HOST=0.0.0.0` only when exposing the service
through a trusted network boundary, and always configure `WEAVEFLOW_MANAGEMENT_TOKEN` for that deployment.

## Compose deployment

Install the Docker Compose plugin, tag or load the packaged image, then copy `scripts/.env.example` to
`scripts/.env`:

```bash
docker tag <IMAGE_ID> weaveflow:0.1.1
cp scripts/.env.example scripts/.env
docker compose --env-file scripts/.env -f scripts/compose.yaml up -d
docker compose --env-file scripts/.env -f scripts/compose.yaml ps
```

View logs or stop the service with:

```bash
docker compose --env-file scripts/.env -f scripts/compose.yaml logs -f weaveflow
docker compose --env-file scripts/.env -f scripts/compose.yaml down
```

This Compose file runs a prebuilt image; it never accesses the source tree to build one. The current local
version is recorded in `VERSION` and defaults to `weaveflow:0.1.1`; set `WEAVEFLOW_IMAGE` in `scripts/.env` when the
image uses another tag. Compose uses the same loopback-only binding,
read-only root filesystem, non-root image, health check, data volume, and workspace mount as `scripts/deploy.sh`. Set
`WEAVEFLOW_PUBLISH_HOST=0.0.0.0` in `scripts/.env` only behind a trusted network boundary and keep
`WEAVEFLOW_MANAGEMENT_TOKEN` configured.

## Container settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `WEAVEFLOW_WEB_PORT` | `8080` | WebUI and reverse-proxy port inside the container |
| `WEAVEFLOW_SERVER_PORT` | `8081` | Internal server port |
| `WEAVEFLOW_WEB_BACKEND_URL` | `/api` | Browser backend URL; same-origin by default |
| `WEAVEFLOW_CORS_ORIGINS` | Localhost URLs for `<web-port>` | Allowed cross-origin browser origins |
| `WEAVEFLOW_DATA_DIR` | `/data/server` | Persistent runtime data directory |
| `WEAVEFLOW_SECRET_DIR` | `/data/secrets` | File-backed secret directory |
| `WEAVEFLOW_GRAPH` | empty | Optional graph JSON file to preload |
| `WEAVEFLOW_SERVER_PREFIX` | empty | Optional route prefix, for example `/debug` |
| `WEAVEFLOW_LOG_LEVEL` | `info` | `debug`, `info`, or `error` |
| `WEAVEFLOW_MANAGEMENT_TOKEN` | empty | Bearer token for management API protection |

All other server environment variables, including `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`, and the
`WEAVEFLOW_CODEX_*` and `WEAVEFLOW_CLAUDE_*` settings, can be supplied through the env file.

The data volume stores graphs, runs, checkpoints, artifacts, triggers, and managed secrets. The workspace mount is
writable in the examples because agent tools may edit files; use `:ro` when the deployed workflow does not need file
writes.

The image exposes `GET /healthz` for container health checks. It does not require the management token and returns the
server status, version, and UTC build time. The WebUI configuration is generated under `/tmp` at startup, so the image
can run with a read-only root filesystem.

For a published release, create a Git tag such as `v0.1.1` after committing the release changes. The GitHub Release
workflow removes the leading `v` and publishes matching Docker tags such as `0.1.1`, `0.1`, `0`, and `latest` for a
stable release.
