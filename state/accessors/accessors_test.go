package accessors

import (
	"testing"

	"github.com/dengzii/weaveflow/state"

	"github.com/tmc/langchaingo/llms"
)

func TestInstallDefaultAccessorsAndUseDomainAccessors(t *testing.T) {
	t.Parallel()

	registry := state.NewRegistry()
	if err := InstallDefaultAccessors(registry); err != nil {
		t.Fatalf("install default accessors: %v", err)
	}

	access := state.NewEditingAccess(registry, state.NewState())

	request, err := state.UseAccessor(access, RequestID)
	if err != nil {
		t.Fatalf("use request accessor: %v", err)
	}
	if err := request.SetInput("build v2"); err != nil {
		t.Fatalf("set request input: %v", err)
	}
	if err := request.SetMetadata(map[string]any{"source": "test"}); err != nil {
		t.Fatalf("set request metadata: %v", err)
	}

	execution, err := state.UseAccessor(access, ExecutionID)
	if err != nil {
		t.Fatalf("use execution accessor: %v", err)
	}
	if err := execution.SetStepResult("step-1", map[string]any{"status": "done"}); err != nil {
		t.Fatalf("set step result: %v", err)
	}
	if err := execution.SetLastLLMStepID("step-1"); err != nil {
		t.Fatalf("set last llm step: %v", err)
	}

	final, err := state.UseAccessor(access, FinalID)
	if err != nil {
		t.Fatalf("use final accessor: %v", err)
	}
	if err := final.SetAnswer("done"); err != nil {
		t.Fatalf("set final answer: %v", err)
	}

	observations, err := state.UseAccessor(access, ObservationsID)
	if err != nil {
		t.Fatalf("use observations accessor: %v", err)
	}
	if err := observations.Append(map[string]any{"kind": "note"}); err != nil {
		t.Fatalf("append observation: %v", err)
	}

	planner, err := state.UseAccessor(access, PlannerID)
	if err != nil {
		t.Fatalf("use planner accessor: %v", err)
	}
	if err := planner.SetField("status", "ready"); err != nil {
		t.Fatalf("set planner status: %v", err)
	}

	if request.Input() != "build v2" {
		t.Fatalf("unexpected request input %q", request.Input())
	}
	if execution.LastLLMStepID() != "step-1" {
		t.Fatalf("unexpected last llm step %q", execution.LastLLMStepID())
	}
	if final.Answer() != "done" {
		t.Fatalf("unexpected final answer %q", final.Answer())
	}
	if len(observations.Items()) != 1 || observations.Items()[0]["kind"] != "note" {
		t.Fatalf("unexpected observations %#v", observations.Items())
	}
	if status, ok := planner.Field("status"); !ok || status != "ready" {
		t.Fatalf("unexpected planner status %#v ok=%v", status, ok)
	}
}

func TestConversationAccessorUsesScopeWithoutRootFallback(t *testing.T) {
	t.Parallel()

	registry := state.NewRegistry()
	if err := InstallDefaultAccessors(registry); err != nil {
		t.Fatalf("install default accessors: %v", err)
	}

	rootAccess := state.NewEditingAccess(registry, state.NewState())
	rootConversation, err := state.UseAccessor(rootAccess, ConversationID)
	if err != nil {
		t.Fatalf("use root conversation: %v", err)
	}
	if err := rootConversation.AppendMessage(llms.TextParts(llms.ChatMessageTypeHuman, "root")); err != nil {
		t.Fatalf("append root message: %v", err)
	}

	scopedAccess := rootAccess.WithScope("agent")
	scopedConversation, err := state.UseAccessor(scopedAccess, ConversationID)
	if err != nil {
		t.Fatalf("use scoped conversation: %v", err)
	}
	if len(scopedConversation.Messages()) != 0 {
		t.Fatalf("expected scoped conversation to avoid root fallback, got %#v", scopedConversation.Messages())
	}
	if err := scopedConversation.AppendMessage(llms.TextParts(llms.ChatMessageTypeAI, "scoped")); err != nil {
		t.Fatalf("append scoped message: %v", err)
	}

	if got := len(rootConversation.Messages()); got != 1 {
		t.Fatalf("expected root messages unchanged, got %d", got)
	}
	if got := len(scopedConversation.Messages()); got != 1 {
		t.Fatalf("expected one scoped message, got %d", got)
	}

	contract, ok := registry.AccessorContract(ConversationID.Name(), "agent")
	if !ok {
		t.Fatal("expected conversation contract")
	}
	if len(contract.Fields) != 4 || contract.Fields[0].Path.String() != "scopes.agent.conversation.messages" {
		t.Fatalf("unexpected scoped conversation contract %#v", contract)
	}
}

func TestRecordItemsAreCloned(t *testing.T) {
	t.Parallel()

	registry := state.NewRegistry()
	if err := InstallDefaultAccessors(registry); err != nil {
		t.Fatalf("install default accessors: %v", err)
	}

	access := state.NewEditingAccess(registry, state.NewState())
	evidence, err := state.UseAccessor(access, EvidenceID)
	if err != nil {
		t.Fatalf("use evidence accessor: %v", err)
	}
	if err := evidence.Append(map[string]any{"id": "a"}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}

	items := evidence.Items()
	items[0]["id"] = "mutated"

	itemsAgain := evidence.Items()
	if itemsAgain[0]["id"] != "a" {
		t.Fatalf("expected cloned record item, got %#v", itemsAgain)
	}
}

func TestRecordsAppendUsesAppendPatchAndMergesInParallel(t *testing.T) {
	t.Parallel()

	registry := state.NewRegistry()
	if err := InstallDefaultAccessors(registry); err != nil {
		t.Fatalf("install default accessors: %v", err)
	}
	contract, ok := registry.AccessorContract(EvidenceID.Name())
	if !ok {
		t.Fatal("expected evidence contract")
	}

	base := state.NewState()
	leftAccess := state.NewEditingAccess(registry, base)
	leftEvidence, err := state.UseAccessor(leftAccess, EvidenceID)
	if err != nil {
		t.Fatalf("use left evidence: %v", err)
	}
	if err := leftEvidence.Append(map[string]any{"id": "left"}); err != nil {
		t.Fatalf("append left evidence: %v", err)
	}

	rightAccess := state.NewEditingAccess(registry, base)
	rightEvidence, err := state.UseAccessor(rightAccess, EvidenceID)
	if err != nil {
		t.Fatalf("use right evidence: %v", err)
	}
	if err := rightEvidence.Append(map[string]any{"id": "right"}); err != nil {
		t.Fatalf("append right evidence: %v", err)
	}

	leftOps := leftAccess.Patch().Ops()
	if len(leftOps) != 1 || leftOps[0].Kind != state.OpAppend {
		t.Fatalf("expected left append patch, got %#v", leftOps)
	}

	merged, err := state.MergeParallelPatches(base, []state.BranchPatch{
		{NodeID: "left", Order: 1, Patch: leftAccess.Patch()},
		{NodeID: "right", Order: 2, Patch: rightAccess.Patch()},
	}, state.ParallelMergeOptions{
		Contracts: map[string]state.Contract{
			"left":  contract,
			"right": contract,
		},
	})
	if err != nil {
		t.Fatalf("merge evidence patches: %v", err)
	}

	mergedEvidence, err := state.UseAccessor(state.NewAccess(registry, merged), EvidenceID)
	if err != nil {
		t.Fatalf("use merged evidence: %v", err)
	}
	items := mergedEvidence.Items()
	if len(items) != 2 || items[0]["id"] != "left" || items[1]["id"] != "right" {
		t.Fatalf("unexpected merged evidence %#v", items)
	}
}
