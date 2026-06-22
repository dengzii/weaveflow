package state

import "testing"

func TestTypedRefAccessRecordsPatchAndKeepsBaseImmutable(t *testing.T) {
	t.Parallel()

	inputRef := NewRef[string](Shared("request", "input")).Required()
	base := FromShared(map[string]any{
		"request": map[string]any{"input": "old"},
	})
	access := NewEditingAccess(nil, base)

	got, ok := Read(access, inputRef)
	if !ok || got != "old" {
		t.Fatalf("expected old input, got %q ok=%v", got, ok)
	}
	if err := Set(access, inputRef, "new"); err != nil {
		t.Fatalf("set input: %v", err)
	}

	updated, ok := Read(access, inputRef)
	if !ok || updated != "new" {
		t.Fatalf("expected updated input from working state, got %q ok=%v", updated, ok)
	}
	unchanged, ok := Read(NewAccess(nil, base), inputRef)
	if !ok || unchanged != "old" {
		t.Fatalf("expected base state to remain unchanged, got %q ok=%v", unchanged, ok)
	}

	ops := access.Patch().Ops()
	if len(ops) != 1 {
		t.Fatalf("expected one patch op, got %#v", ops)
	}
	if ops[0].Kind != OpSet || ops[0].Path.String() != "shared.request.input" || ops[0].Value != "new" {
		t.Fatalf("unexpected patch op %#v", ops[0])
	}

	replayed, err := access.Patch().Apply(base)
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	replayedValue, ok := Read(NewAccess(nil, replayed), inputRef)
	if !ok || replayedValue != "new" {
		t.Fatalf("expected replayed state to contain new input, got %q ok=%v", replayedValue, ok)
	}
}

func TestPatchOperationsAreExplicit(t *testing.T) {
	t.Parallel()

	tagsRef := NewRef[[]string](Shared("tags"))
	base := FromShared(map[string]any{
		"tags": []string{"alpha"},
		"meta": map[string]any{
			"left": "keep",
		},
		"stale": true,
	})
	access := NewEditingAccess(nil, base)

	if err := access.AppendAny(tagsRef.Path(), "beta"); err != nil {
		t.Fatalf("append tags: %v", err)
	}
	if err := access.MergeAny(Shared("meta"), map[string]any{"right": "add"}); err != nil {
		t.Fatalf("merge meta: %v", err)
	}
	if err := access.Delete(Shared("stale")); err != nil {
		t.Fatalf("delete stale: %v", err)
	}

	tags, ok := Read(access, tagsRef)
	if !ok || len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("unexpected tags %#v ok=%v", tags, ok)
	}
	metaValue, ok := access.ReadAny(Shared("meta"))
	if !ok {
		t.Fatal("expected merged meta")
	}
	meta := metaValue.(map[string]any)
	if meta["left"] != "keep" || meta["right"] != "add" {
		t.Fatalf("unexpected merged meta %#v", meta)
	}
	if _, ok := access.ReadAny(Shared("stale")); ok {
		t.Fatal("expected stale field to be deleted")
	}

	ops := access.Patch().Ops()
	if len(ops) != 3 {
		t.Fatalf("expected three patch ops, got %#v", ops)
	}
	if ops[0].Kind != OpAppend || ops[1].Kind != OpMerge || ops[2].Kind != OpDelete {
		t.Fatalf("unexpected op order %#v", ops)
	}
}

func TestTypedRefOperationsRecordExplicitPatchKinds(t *testing.T) {
	t.Parallel()

	tagsRef := NewRef[[]string](Shared("tags")).WithMerge(MergeAppend)
	metaRef := NewRef[map[string]any](Shared("meta")).WithMerge(MergeMerge)
	staleRef := NewRef[bool](Shared("stale"))
	access := NewEditingAccess(nil, FromShared(map[string]any{
		"tags":  []string{"alpha"},
		"meta":  map[string]any{"left": "keep"},
		"stale": true,
	}))

	if err := Append(access, tagsRef, "beta"); err != nil {
		t.Fatalf("append tags: %v", err)
	}
	if err := Merge(access, metaRef, map[string]any{"right": "add"}); err != nil {
		t.Fatalf("merge meta: %v", err)
	}
	if err := Delete(access, staleRef); err != nil {
		t.Fatalf("delete stale: %v", err)
	}

	tags, err := Get(access, tagsRef)
	if err != nil {
		t.Fatalf("get tags: %v", err)
	}
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("unexpected tags %#v", tags)
	}

	meta, err := Get(access, metaRef)
	if err != nil {
		t.Fatalf("get meta: %v", err)
	}
	if meta["left"] != "keep" || meta["right"] != "add" {
		t.Fatalf("unexpected meta %#v", meta)
	}

	ops := access.Patch().Ops()
	if len(ops) != 3 {
		t.Fatalf("expected three patch ops, got %#v", ops)
	}
	if ops[0].Kind != OpAppend || ops[1].Kind != OpMerge || ops[2].Kind != OpDelete {
		t.Fatalf("unexpected op order %#v", ops)
	}
}

func TestGetReportsMissingAndTypeMismatchSeparately(t *testing.T) {
	t.Parallel()

	nameRef := NewRef[string](Shared("name"))
	countRef := NewRef[int](Shared("count"))
	access := NewAccess(nil, FromShared(map[string]any{"count": "not-int"}))

	if _, err := Get(access, nameRef); err == nil || err.Error() != `state path "shared.name" is missing` {
		t.Fatalf("unexpected missing error %v", err)
	}
	if _, err := Get(access, countRef); err == nil || err.Error() != `state path "shared.count" type mismatch: got string, want int` {
		t.Fatalf("unexpected type mismatch error %v", err)
	}
	if _, err := ReadRequired(access, countRef); err == nil || err.Error() != `state path "shared.count" type mismatch: got string, want int` {
		t.Fatalf("unexpected required read error %v", err)
	}
}

type testCounter interface {
	Count() int
	Add(delta int) error
}

type testCounterAccessor struct {
	access *Access
	ref    Ref[int]
}

func (c testCounterAccessor) Count() int {
	value, _ := Read(c.access, c.ref)
	return value
}

func (c testCounterAccessor) Add(delta int) error {
	return Replace(c.access, c.ref, c.Count()+delta)
}

type testCounterExtension struct{}

var testCounterID = NewAccessorID[testCounter]("counter")

func (testCounterExtension) Name() string { return "counter" }

func (testCounterExtension) Install(registry *Registry) error {
	return registry.RegisterAccessor(AccessorDefinition{
		Name: "counter",
		ContractFactory: func(scope string) Contract {
			return NewContract(testCounterRef(scope).ReadWriteField())
		},
		Factory: func(access *Access) any {
			return testCounterAccessor{
				access: access,
				ref:    testCounterRef(access.Scope()),
			}
		},
	})
}

func testCounterRef(scope string) Ref[int] {
	if scope == "" {
		return NewRef[int](Shared("counter", "count")).Required()
	}
	return NewRef[int](Scope(scope, "counter", "count")).Required()
}

func TestRegistryInstallsScopedAccessorExtension(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Install(testCounterExtension{}); err != nil {
		t.Fatalf("install counter extension: %v", err)
	}

	contract, ok := registry.AccessorContract("counter", "agent")
	if !ok {
		t.Fatal("expected counter contract")
	}
	if len(contract.Fields) != 1 || contract.Fields[0].Path.String() != "scopes.agent.counter.count" {
		t.Fatalf("unexpected scoped contract %#v", contract)
	}

	access := NewEditingAccess(registry, NewState()).WithScope("agent")
	counter, err := UseAccessor(access, testCounterID)
	if err != nil {
		t.Fatalf("use counter accessor: %v", err)
	}
	if counter.Count() != 0 {
		t.Fatalf("expected missing counter to read as zero, got %d", counter.Count())
	}
	if err := counter.Add(3); err != nil {
		t.Fatalf("add counter: %v", err)
	}
	if counter.Count() != 3 {
		t.Fatalf("expected counter 3, got %d", counter.Count())
	}

	ops := access.Patch().Ops()
	if len(ops) != 1 || ops[0].Path.String() != "scopes.agent.counter.count" || ops[0].Value != 3 {
		t.Fatalf("unexpected counter patch %#v", ops)
	}
}
