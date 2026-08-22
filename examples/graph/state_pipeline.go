//go:build ignore

package main

import (
	"context"
	"fmt"

	"github.com/dengzii/weaveflow"
	"github.com/dengzii/weaveflow/core"
	wfgraph "github.com/dengzii/weaveflow/graph"
	"github.com/dengzii/weaveflow/node"
	"github.com/dengzii/weaveflow/node/stateops"
	"github.com/dengzii/weaveflow/state"
)

func main() {
	ctx := context.Background()
	workflow := weaveflow.NewGraph()

	// 1. Prepare - initialize document state.
	addFuncNode(workflow, "prepare", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("doc"), map[string]any{
			"title": "my document",
			"body":  "hello world",
			"score": 42,
			"tags":  []string{"draft"},
		})
	})
	addFuncNode(workflow, "prepare_meta", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("meta"), map[string]any{
			"author":  "weaveflow",
			"version": 2,
		})
	})
	addFuncNode(workflow, "prepare_extra", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		return core.Success(), access.SetAny(state.Shared("new_tag"), "reviewed")
	})

	// 2. state_set - replace the document title.
	setTitle := stateops.NewStateSetNode("final report", core.WithID("set_title"))
	setTitle.TargetPath = state.Shared("doc", "title")
	must(workflow.AddNode(setTitle))

	// 3. state_merge - deep-merge metadata into the document.
	mergeMeta := stateops.NewStateMergeNode(core.WithID("merge_meta"))
	mergeMeta.SourcePath = state.Shared("meta")
	mergeMeta.TargetPath = state.Shared("doc")
	must(workflow.AddNode(mergeMeta))

	// 4. state_copy - duplicate the document for backup.
	copyDoc := stateops.NewStateCopyNode(core.WithID("backup"))
	copyDoc.SourcePath = state.Shared("doc")
	copyDoc.TargetPath = state.Shared("enriched")
	must(workflow.AddNode(copyDoc))

	// 5. state_append - add a tag to the document tags array.
	addTag := stateops.NewStateAppendNode(core.WithID("add_tag"))
	addTag.SourcePath = state.Shared("new_tag")
	addTag.TargetPath = state.Shared("doc", "tags")
	must(workflow.AddNode(addTag))

	// 6. state_transform - compute bonus score with CEL: score * 2.
	compute, err := stateops.NewStateTransformNode(`inputs.score * 2`, core.WithID("compute_score"))
	must(err)
	compute.InputPaths = map[string]state.Path{"score": state.Shared("doc", "score")}
	compute.OutputPath = state.Shared("doc", "bonus")
	must(workflow.AddNode(compute))

	// 7. state_delete - remove temporary data.
	cleanup := stateops.NewStateDeleteNode(core.WithID("cleanup_tmp"))
	cleanup.TargetPath = state.Shared("new_tag")
	must(workflow.AddNode(cleanup))

	// 8. Report - read and print the final state.
	addFuncNode(workflow, "report", func(_ core.Context, access *state.Access) (core.NodeResult, error) {
		doc, _ := access.ReadAny(state.Shared("doc"))
		enriched, _ := access.ReadAny(state.Shared("enriched"))
		_, tempExists := access.ReadAny(state.Shared("new_tag"))
		fmt.Println("=== Pipeline Result ===")
		fmt.Printf("doc      = %#v\n", doc)
		fmt.Printf("enriched = %#v\n", enriched)
		fmt.Printf("temp_cleaned = %v\n", !tempExists)
		return core.Success(), nil
	})

	// Wire topology.
	must(workflow.SetEntryPoint("prepare"))
	must(workflow.SetFinishPoint("report"))
	must(workflow.AddEdge("prepare", "prepare_meta"))
	must(workflow.AddEdge("prepare_meta", "prepare_extra"))
	must(workflow.AddEdge("prepare_extra", "set_title"))
	must(workflow.AddEdge("set_title", "merge_meta"))
	must(workflow.AddEdge("merge_meta", "backup"))
	must(workflow.AddEdge("backup", "add_tag"))
	must(workflow.AddEdge("add_tag", "compute_score"))
	must(workflow.AddEdge("compute_score", "cleanup_tmp"))
	must(workflow.AddEdge("cleanup_tmp", "report"))

	// Execute.
	finalState, err := workflow.Run(ctx, state.NewState())
	must(err)

	doc, _ := state.NewAccess(finalState).ReadAny(state.Shared("doc"))
	fmt.Printf("\nFinal doc: %#v\n", doc)
}

func addFuncNode(workflow *wfgraph.Graph, id string, fn node.ExecuteFunc) {
	must(workflow.AddNode(node.NewFuncNode(node.Spec{ID: id, Name: id}, fn)))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
