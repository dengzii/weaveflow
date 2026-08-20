## Summary

<!-- What changed and why? Keep this concise. -->

## Testing

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `bun test` (when changing `internal/web`)
- [ ] `bun run build` (when changing `internal/web`)
- [ ] Container build or health check (when changing `scripts/`)

## Risk and rollout

<!-- Describe compatibility, migration, configuration, or rollout concerns. -->

## Checklist

- [ ] I removed secrets and private data from logs, examples, and screenshots.
- [ ] I updated documentation for user-visible behavior.
- [ ] I added or updated tests for behavior changes.
- [ ] I considered backward compatibility for graph and runtime data formats.
