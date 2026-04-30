package main

import (
	"context"
	"fmt"
	"path/filepath"
	"weaveflow/memory"
	"weaveflow/nodes"
	"weaveflow/runtime"
)

func MemoryRecallExample() {
	repo := memory.NewFileMemoryRepository(filepath.Join(".local", "example"))
	mgr := memory.New(&memory.Options{
		Repository: repo,
		Retriever:  memory.NewBM25Retriever(repo, nil),
	})

	must(mgr.Store([]memory.Entry{
		{Text: "User prefers concise bullet-point answers.", Role: "assistant", Type: memory.EntryTypeFact, Tags: []string{"preference"}},
		{Text: "Previous session discussed IM message routing design.", Role: "assistant", Type: memory.EntryTypeSummary, Tags: []string{"final_answer", "assistant_output"}},
		{Text: "The signaling system uses a publish-subscribe pattern.", Role: "assistant", Type: memory.EntryTypeFact, Tags: []string{"architecture"}},
	}))

	svc := &runtime.Services{Memory: mgr}
	ctx := runtime.WithServices(context.Background(), svc)

	node := nodes.NewMemoryRecallNode()
	node.Limit = 3
	node.Tags = []string{"final_answer", "assistant_output", "preference"}

	state := runtime.State{
		"request": map[string]any{
			"input": "消息路由的设计细节是什么?",
		},
		"orchestration": map[string]any{
			"use_memory":   true,
			"memory_query": "IM message routing design",
		},
	}

	fmt.Println("input:")
	fmt.Println(state["request"])

	result, err := node.Invoke(ctx, state)
	must(err)

	fmt.Println()
	fmt.Println("memory state:")
	printJSON(result.Get(runtime.StateKeyMemory))
}
