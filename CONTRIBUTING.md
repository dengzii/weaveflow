# Contributing to WeaveFlow

Thanks for helping improve WeaveFlow. Please search existing issues before opening a new one and keep pull requests
focused on one change.

## Development setup

- Go `1.26.1` or newer
- Bun `1.3.4` or newer for the WebUI
- Docker when changing the container or deployment scripts

Run the relevant checks from the repository root:

```bash
go test ./...
go vet ./...
cd internal/web && bun install --frozen-lockfile && bun test && bun run build
docker build -f scripts/Dockerfile -t weaveflow:local .
```

Do not commit `.env` files, credentials, private graph data, generated `internal/web/dist`, or dependency folders.

## Pull requests

Explain the motivation, implementation, tests, compatibility impact, and rollout concerns. User-visible behavior and
configuration changes should include documentation. Keep commits reviewable and avoid unrelated formatting changes.

## Releases

Maintainers publish releases by pushing an annotated tag such as `v1.2.3`. The release workflow verifies the source,
builds cross-platform server archives, publishes GitHub release notes, signs and attests the Docker image, and updates
the Docker Hub description.
