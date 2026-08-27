# Quick Start

This guide takes you from a clean checkout to an inspectable Run. Start with the model-free path to learn the runtime,
then configure a provider when you are ready to build an agent workflow.

## Requirements

- Go 1.26 or newer
- Git
- An OpenAI-compatible endpoint for examples that call a model

Check the toolchain before starting:

```bash
go version
git --version
```

## Install the module

```bash
go get github.com/dengzii/weaveflow
```

To work from the repository:

```bash
git clone https://github.com/dengzii/weaveflow.git
cd weaveflow
go build ./...
```

The module is currently developed against Go `1.26.6`; use the latest compatible `1.26.x` release when possible.

## Run without a model

No credentials are needed for the model-free examples:

```bash
go run ./examples/graph/conditional_routing.go
```

Try the other runtime controls to see different lifecycle behaviors:

```bash
go run ./examples/graph/dynamic_map_reduce.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
go run ./examples/graph/fan_in_fan_out.go
```

These programs build a graph, execute it with local state, and print the result. Read [Runtime Model](/concepts/runtime)
to connect the output to Runs, Steps, Events, and Checkpoints.

## Configure a model

The default graph example reads an OpenAI-compatible model configuration from environment variables:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.example.com/v1"
export OPENAI_MODEL="your-model"
```

For a local OpenAI-compatible service, use its API root (for example `http://localhost:8000/v1`). Do not include a
credential in a Graph Definition or commit the shell file containing it.

PowerShell:

```powershell
$env:OPENAI_API_KEY = "your-api-key"
$env:OPENAI_BASE_URL = "https://api.example.com/v1"
$env:OPENAI_MODEL = "your-model"
```

## Run the graph example

```bash
go run ./examples/graph
```

The example builds a ReAct-style graph, persists its definition, records execution data under `.local/instance/`, and
demonstrates checkpoint resume.

The first invocation creates a local Run. The example then loads the saved Graph Definition, locates a continuable Run,
and resumes it with new input. Remove `.local/instance/` to start with a clean local store.

## Start the Debug Server

The server exposes the Graph, Session, Run, and registry APIs used by the Workbench:

```bash
go run ./cmd/server -addr 127.0.0.1:8080 -data .local/server \
  -graph examples/codespaces_demo/graph.json
```

Check that it is ready:

```bash
curl http://127.0.0.1:8080/healthz
```

Start the [Workbench](/guides/workbench) in another terminal to edit the graph and inspect Runs.

## What to learn next

1. Read [Graph Definition](/concepts/graph-definition) to understand versioning, nodes, edges, and policies.
2. Read [State Bindings](/concepts/state-bindings) before changing a node or adding a parallel branch.
3. Use [Runnable Examples](/guides/examples) to map concepts to executable files.
4. Configure a provider with [Model Providers](/guides/model-providers) when you are ready for model calls.
5. Deploy the packaged server with [Deploy the Server](/deployment).
