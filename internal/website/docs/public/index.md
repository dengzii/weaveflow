---
layout: home

hero:
  name: WeaveFlow
  text: Deterministic agent workflows in Go
  tagline: Build explicit graphs, constrain state access, inspect every run, and recover from checkpoints.
  actions:
    - theme: brand
      text: Get started
      link: /getting-started
    - theme: alt
      text: Open playground
      link: https://playground.weaveflow.space

features:
  - title: Explicit graph topology
    details: Define serializable nodes, edges, conditions, and entry points instead of hiding control flow in an agent loop.
  - title: Contract-bound state
    details: Bind State Ports to explicit paths and validate reads, writes, capabilities, and parallel merge behavior before execution.
  - title: Inspectable runtime
    details: Persist Runs, Events, Checkpoints, and Artifacts so execution can be inspected, paused, resumed, and recovered.
---

## What is WeaveFlow?

WeaveFlow is a graph runtime for building, executing, and inspecting LLM agents in Go. It combines a declarative Graph
Definition, deterministic execution, checkpointed state, and reusable nodes for model calls, tool use, planning,
routing, and human approval.

The project is local-first: the runtime and Workbench can run on your own machine, while Graph Definitions remain
portable JSON documents.

## Choose a path

- Follow [Getting Started](/getting-started) to install the module and run an example.
- Learn how [Graph Definitions](/concepts/graph-definition) describe topology.
- Understand [State Bindings](/concepts/state-bindings) before building custom nodes.
- Use the [Workbench](/guides/workbench) to edit graphs and inspect runs.
