# Codespaces demo

This profile provides a disposable, credential-free Workbench demo for WeaveFlow. Use the
[one-click Codespaces entry point](../../README.md#one-click-workbench-demo) from the repository README; it pulls the
prebuilt demo image, forwards the Workbench on port `8080`, and opens it in the browser.

## What it includes

The profile uses its own `Dockerfile` and `docker-entrypoint.sh`. The image is published to
`ghcr.io/dengzii/weaveflow-codespaces:main` by the Codespaces Demo workflow when files used by the image change on
`master`, then pulled by the Dev Container configuration. It is separate from the production-oriented image documented in
[`scripts/README.md`](../../scripts/README.md). The image packages the WebUI, the Debug Server, and
[`examples/codespaces_demo/graph.json`](../../examples/codespaces_demo/graph.json). On startup, the entrypoint publishes
that definition as an immutable Graph Session before exposing the WebUI, so `codespaces_demo` appears in the Workbench
Graph list and can run with the default empty State.

The repository is mounted at `/workspace`, which is also the workspace root for bundled file tools. Runtime data and
managed secrets are stored under `/tmp/weaveflow-codespaces`; they are intended to be disposable and are not a
persistent deployment volume. The demo does not require a model provider.

## Bootstrap settings

The image can publish a different definition that is packaged into the image by setting:

| Variable | Purpose |
| --- | --- |
| `WEAVEFLOW_BOOTSTRAP_GRAPH` | Path to the packaged Graph Definition JSON file. |
| `WEAVEFLOW_BOOTSTRAP_GRAPH_ID` | Graph ID to create or reuse. |
| `WEAVEFLOW_BOOTSTRAP_GRAPH_VERSION` | Optional Graph version; defaults to `1.0`. |

Bootstrap IDs and versions accept letters, numbers, `.`, `_`, `~`, and `-`. If the Graph already exists, later
container starts leave it unchanged.

## Security

Port `8080` is intended to remain private to the Codespaces environment. Configure `WEAVEFLOW_MANAGEMENT_TOKEN`
before making the forwarded port public. The GHCR package must be public if anonymous users should launch the one-click
profile.
