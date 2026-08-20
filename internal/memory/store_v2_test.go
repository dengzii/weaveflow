package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryNamespaceAndVersion(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	first, err := store.Put(ctx, Namespace("user-a"), MemoryRecord{Key: "profile", Content: "alpha"}, "")
	if err != nil {
		t.Fatalf("Put() = %v", err)
	}
	if first.Version != "1" {
		t.Fatalf("first version = %q, want 1", first.Version)
	}
	if _, err := store.Put(ctx, Namespace("user-a"), MemoryRecord{Key: "profile", Content: "stale"}, ""); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale Put() error = %v, want version conflict", err)
	}
	second, err := store.Put(ctx, Namespace("user-a"), MemoryRecord{Key: "profile", Content: "beta"}, first.Version)
	if err != nil {
		t.Fatalf("versioned Put() = %v", err)
	}
	if second.Version != "2" {
		t.Fatalf("second version = %q, want 2", second.Version)
	}
	if _, err := store.Get(ctx, Namespace("user-b"), "profile"); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("cross-namespace Get() error = %v, want not found", err)
	}
}

func TestMemorySearchPaginationAndIsolation(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	for _, item := range []struct {
		namespace Namespace
		key       string
		content   string
	}{
		{Namespace("a"), "one", "graph runtime"},
		{Namespace("a"), "two", "graph state"},
		{Namespace("a"), "three", "unrelated"},
		{Namespace("b"), "secret", "graph runtime"},
	} {
		if _, err := store.Put(ctx, item.namespace, MemoryRecord{Key: item.key, Content: item.content}, ""); err != nil {
			t.Fatalf("Put(%q): %v", item.key, err)
		}
	}
	page, err := store.Search(ctx, Namespace("a"), MemoryQuery{Text: "graph", Limit: 1})
	if err != nil {
		t.Fatalf("Search() = %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].Namespace != Namespace("a") || page.NextCursor == "" {
		t.Fatalf("Search() page = %#v, want one namespaced record and cursor", page)
	}
	next, err := store.Search(ctx, Namespace("a"), MemoryQuery{Text: "graph", Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("Search() next page = %v", err)
	}
	if len(next.Records) != 1 || next.Records[0].Key == page.Records[0].Key {
		t.Fatalf("Search() next page = %#v, want different record", next)
	}
}

func TestMemoryRetentionAndRecovery(t *testing.T) {
	temporary := t.TempDir()
	path := filepath.Join(temporary, "memory.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore() = %v", err)
	}
	deadline := time.Now().UTC().Add(-time.Minute)
	if _, err := store.Put(context.Background(), Namespace("session"), MemoryRecord{Key: "expired", Content: "old", RetainUntil: &deadline}, ""); err != nil {
		t.Fatalf("Put(expired) = %v", err)
	}
	if _, err := store.Put(context.Background(), Namespace("session"), MemoryRecord{Key: "kept", Content: "new"}, ""); err != nil {
		t.Fatalf("Put(kept) = %v", err)
	}
	removed, err := store.PurgeExpired(context.Background(), time.Now().UTC(), 0)
	if err != nil || removed != 1 {
		t.Fatalf("PurgeExpired() = %d, %v; want 1", removed, err)
	}
	restarted, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload NewFileStore() = %v", err)
	}
	if _, err := restarted.Get(context.Background(), Namespace("session"), "expired"); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expired Get() error = %v, want not found", err)
	}
	if _, err := restarted.Get(context.Background(), Namespace("session"), "kept"); err != nil {
		t.Fatalf("kept Get() = %v", err)
	}
}

func TestMemoryDeleteRollsBackWhenPersistenceFails(t *testing.T) {
	temporary := t.TempDir()
	path := filepath.Join(temporary, "memory.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore() = %v", err)
	}
	created, err := store.Put(context.Background(), Namespace("session"), MemoryRecord{Key: "keep", Content: "value"}, "")
	if err != nil {
		t.Fatalf("Put() = %v", err)
	}
	versioned := store.(*VersionedStore)
	versioned.filePath = temporary
	if err := store.Delete(context.Background(), Namespace("session"), "keep", created.Version); err == nil {
		t.Fatal("Delete() error = nil, want persistence failure")
	}
	restored, err := store.Get(context.Background(), Namespace("session"), "keep")
	if err != nil {
		t.Fatalf("Get() after failed Delete = %v", err)
	}
	if restored.Version != created.Version || restored.DeletedAt != nil {
		t.Fatalf("record after failed Delete = %#v, want original record", restored)
	}
}

func TestMemoryPutDoesNotExposePartialFileOnPersistenceFailure(t *testing.T) {
	temporary := t.TempDir()
	path := filepath.Join(temporary, "memory.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore() = %v", err)
	}
	if _, err := store.Put(context.Background(), Namespace("session"), MemoryRecord{Key: "stable", Content: "before"}, ""); err != nil {
		t.Fatalf("initial Put() = %v", err)
	}
	versioned := store.(*VersionedStore)
	versioned.filePath = temporary
	if _, err := store.Put(context.Background(), Namespace("session"), MemoryRecord{Key: "partial", Content: "should not appear"}, ""); err == nil {
		t.Fatal("failed Put() error = nil, want persistence failure")
	}
	versioned.filePath = path
	restarted, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload NewFileStore() = %v", err)
	}
	if _, err := restarted.Get(context.Background(), Namespace("session"), "stable"); err != nil {
		t.Fatalf("stable record after failed Put = %v", err)
	}
	if _, err := restarted.Get(context.Background(), Namespace("session"), "partial"); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("partial record after failed Put = %v, want not found", err)
	}
}
