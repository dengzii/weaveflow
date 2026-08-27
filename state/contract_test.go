package state

import (
	"reflect"
	"testing"
)

func TestContractCloneAndRefMetadataRemainIndependent(t *testing.T) {
	ref := NewRef[[]string](Shared("results"))
	ref = ref.Required().WithMerge(MergeAppend).WithReducer("append.v1").WithDescription("collected results")
	field := ref.ReadWriteField()
	field.Schema = JSONSchema{
		"type":  "array",
		"items": JSONSchema{"type": "string"},
	}
	contract := NewContract(field, field, NewRef[string](Shared("input")).ReadField())
	cloned := contract.Clone()

	cloned.Fields[0].Schema["type"] = "object"
	cloned.Fields[0].Path = Shared("changed")
	if contract.Fields[0].Schema["type"] != "array" || contract.Fields[0].Path.String() != "shared.results" {
		t.Fatalf("Contract.Clone() aliased fields: %#v", contract.Fields[0])
	}
	if !reflect.DeepEqual(field.Path, ref.Path()) || !field.Required || field.Merge != MergeAppend || field.Reducer != "append.v1" || field.Description != "collected results" || field.Type != "[]string" {
		t.Fatalf("ref field metadata = %#v", field)
	}
	if got := contract.ReadPaths(); !reflect.DeepEqual(got, []Path{Shared("results"), Shared("input")}) {
		t.Fatalf("ReadPaths() = %#v", got)
	}
	if got := contract.WritePaths(); !reflect.DeepEqual(got, []Path{Shared("results")}) {
		t.Fatalf("WritePaths() = %#v", got)
	}
}

func TestPathAccessorsAndChildValidation(t *testing.T) {
	path := Shared("request")
	child, err := path.Child("input")
	if err != nil {
		t.Fatalf("Child() error = %v", err)
	}
	if child.Section() != SectionShared || !reflect.DeepEqual(child.Segments(), []string{"request", "input"}) {
		t.Fatalf("child path = %#v", child)
	}
	segments := child.Segments()
	segments[0] = "mutated"
	if child.String() != "shared.request.input" {
		t.Fatalf("Segments() returned aliased data: %q", child.String())
	}
	if _, err := path.Child("bad.segment"); err == nil {
		t.Fatal("Child() accepted a dotted segment")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustChild() did not panic for an invalid segment")
		}
	}()
	_ = path.MustChild("")
}
