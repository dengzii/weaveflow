# Getting Started

## Requirements

- Go 1.26 or newer
- Git
- An OpenAI-compatible endpoint for examples that call a model

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

## Configure a model

The default graph example reads an OpenAI-compatible model configuration from environment variables:

```bash
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=<your-base-url>
export OPENAI_MODEL=<your-model>
```

PowerShell:

```powershell
$env:OPENAI_API_KEY = "<your-api-key>"
$env:OPENAI_BASE_URL = "<your-base-url>"
$env:OPENAI_MODEL = "<your-model>"
```

## Run the graph example

```bash
go run ./examples/graph
```

The example builds a ReAct-style graph, persists its definition, records execution data under `.local/instance/`, and
demonstrates checkpoint resume.

## Run a model-free example

Use a model-free example to explore runtime behavior without credentials:

```bash
go run ./examples/graph/conditional_routing.go
go run ./examples/graph/failure_fallback.go
go run ./examples/graph/human_approval.go
```

Next, read [Graph Definition](/concepts/graph-definition) and [State Bindings](/concepts/state-bindings).
