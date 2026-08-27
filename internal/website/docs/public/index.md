---
layout: home

hero:
  name: WeaveFlow
  text: Build inspectable agent workflows in Go
  tagline: Build explicit graphs, constrain state access, inspect every run, and recover from checkpoints
  actions:
    - theme: brand
      text: Get started
      link: /getting-started
    - theme: alt
      text: Open playground
      link: https://playground.weaveflow.space

features:
  - title: Explicit graph topology
    details: Define serializable nodes, edges, conditions, and entry points instead of hiding control flow in an agent loop
  - title: Contract-bound state
    details: Bind State Ports to explicit paths and validate reads, writes, capabilities, and parallel merge behavior before execution
  - title: Inspectable runtime
    details: Persist Runs, Events, Checkpoints, and Artifacts so execution can be inspected, paused, resumed, and recovered
---

## What is WeaveFlow?

WeaveFlow is a graph runtime written in Go for building, executing, and inspecting LLM agent workflows. It combines a declarative
Graph Definition with explicit execution, checkpointed state, and reusable nodes for model calls, tool use, planning,
routing, and human approval.

The project is designed to run locally first: the runtime and Workbench can run on your own machine, while Graph Definitions
remain portable JSON documents.

## Choose a path

- New to WeaveFlow? Start with [Why WeaveFlow?](/introduction), then follow the [Quick Start](/getting-started).
- Designing a workflow? Learn [Graph Definitions](/concepts/graph-definition), [State Bindings](/concepts/state-bindings),
  and [Nodes and Tools](/concepts/nodes-and-tools).
- Integrating a model or component? Read [Model Providers](/guides/model-providers) and [Custom Nodes](/guides/custom-nodes).
- Operating a workflow? Use the [Workbench](/guides/workbench), [Observability guide](/guides/observability), and
  [HTTP API reference](/reference/http-api).
- Deploying it? Follow [Deploy the Server](/deployment), then review [Configuration](/reference/configuration) and
  [Troubleshooting](/troubleshooting).
