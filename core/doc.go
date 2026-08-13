// Package core defines the stable execution contracts shared by graph
// construction, node implementations, the registry, the runtime, and tools.
//
// The package contains:
//
//   - node contracts and execution primitives, including Node, NodeBase,
//     NodeResult, Command, and ExecuteNode;
//   - Context and the model, tool, environment, and execution services carried
//     through it;
//   - model invocation, observation, response validation, and usage accounting;
//   - tool definition, permission and approval checks, concurrency control,
//     invocation observation, and input/output validation;
//   - classified execution errors used by retry and runtime policies; and
//   - state-contract validation modes, diagnostics, violations, and initial
//     state requirements shared by graph analysis and runtime enforcement.
//
// These are dependency-neutral primitives that must cross package boundaries.
// Core may depend on foundational packages such as state and llms, but must not
// depend on graph, node, registry, runtime, server, or concrete implementations.
//
// Serializable graph definitions belong to dsl, node type schemas and builders
// belong to registry, concrete node implementations belong to node, graph
// compilation and scheduling belong to graph, and run lifecycle and persistence
// belong to runtime. Add a type to core only when multiple higher-level packages
// need the same stable contract and assigning it to one consumer would reverse
// the dependency direction or create an import cycle.
package core
