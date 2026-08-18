package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/dengzii/weaveflow/state"
)

type EffectClass string

const (
	EffectUnspecified        EffectClass = "unspecified"
	EffectPure               EffectClass = "pure"
	EffectReadOnly           EffectClass = "read_only"
	EffectIdempotentWrite    EffectClass = "idempotent_write"
	EffectNonIdempotentWrite EffectClass = "non_idempotent_write"
	EffectCompensatable      EffectClass = "compensatable"
)

type EffectStatus string

const (
	EffectIntent      EffectStatus = "intent"
	EffectSucceeded   EffectStatus = "succeeded"
	EffectFailed      EffectStatus = "failed"
	EffectUnknown     EffectStatus = "unknown"
	EffectNotApplied  EffectStatus = "not_applied"
	EffectCompensated EffectStatus = "compensated"
)

type EffectOperation struct {
	Key               string       `json:"key"`
	ParentKey         string       `json:"parent_key,omitempty"`
	Kind              string       `json:"kind"`
	Name              string       `json:"name"`
	Class             EffectClass  `json:"class"`
	Status            EffectStatus `json:"status"`
	Attempt           int          `json:"attempt,omitempty"`
	IdempotencyKey    string       `json:"idempotency_key,omitempty"`
	ProviderRequestID string       `json:"provider_request_id,omitempty"`
	Error             string       `json:"error,omitempty"`
}

type EffectJournal interface {
	RecordEffect(context.Context, EffectOperation) error
}

type EffectJournalFunc func(context.Context, EffectOperation) error

func (journal EffectJournalFunc) RecordEffect(ctx context.Context, operation EffectOperation) error {
	return journal(ctx, operation)
}

type EffectCompensationRequest struct {
	Operation  EffectOperation   `json:"operation"`
	Operations []EffectOperation `json:"operations,omitempty"`
}

type EffectCompensator interface {
	CompensateEffect(Context, EffectCompensationRequest, *state.Access) error
}

type effectOperationKey struct{}
type effectJournalKey struct{}

func WithEffectOperation(ctx context.Context, operation EffectOperation) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	operation.Class = NormalizeEffectClass(operation.Class)
	return context.WithValue(ctx, effectOperationKey{}, operation)
}

func EffectOperationFromContext(ctx context.Context) (EffectOperation, bool) {
	if ctx == nil {
		return EffectOperation{}, false
	}
	operation, ok := ctx.Value(effectOperationKey{}).(EffectOperation)
	return operation, ok
}

func WithEffectJournal(ctx context.Context, journal EffectJournal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if journal == nil {
		return ctx
	}
	return context.WithValue(ctx, effectJournalKey{}, journal)
}

func EffectJournalFromContext(ctx context.Context) EffectJournal {
	if ctx == nil {
		return nil
	}
	journal, _ := ctx.Value(effectJournalKey{}).(EffectJournal)
	return journal
}

func IdempotencyKeyFromContext(ctx context.Context) (string, bool) {
	operation, ok := EffectOperationFromContext(ctx)
	key := strings.TrimSpace(operation.IdempotencyKey)
	return key, ok && key != ""
}

func ChildEffectOperation(parent EffectOperation, kind, name, identity string, class EffectClass) EffectOperation {
	operation := EffectOperation{
		ParentKey: strings.TrimSpace(parent.Key),
		Kind:      strings.TrimSpace(kind),
		Name:      strings.TrimSpace(name),
		Class:     NormalizeEffectClass(class),
		Status:    EffectIntent,
		Attempt:   parent.Attempt,
	}
	if operation.ParentKey == "" {
		return operation
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{operation.ParentKey, operation.Kind, operation.Name, strings.TrimSpace(identity)}, "\x00")))
	operation.Key = fmt.Sprintf("operation-%x", digest[:16])
	operation.IdempotencyKey = operation.Key
	return operation
}

func NormalizeEffectClass(class EffectClass) EffectClass {
	switch EffectClass(strings.TrimSpace(string(class))) {
	case EffectPure:
		return EffectPure
	case EffectReadOnly:
		return EffectReadOnly
	case EffectIdempotentWrite:
		return EffectIdempotentWrite
	case EffectNonIdempotentWrite:
		return EffectNonIdempotentWrite
	case EffectCompensatable:
		return EffectCompensatable
	default:
		return EffectUnspecified
	}
}

func IsWriteEffect(class EffectClass) bool {
	switch NormalizeEffectClass(class) {
	case EffectIdempotentWrite, EffectNonIdempotentWrite, EffectCompensatable:
		return true
	default:
		return false
	}
}

type EffectDeclarer interface {
	EffectClass() EffectClass
}

func NodeEffectClass(node Node) EffectClass {
	declarer, ok := node.(EffectDeclarer)
	if !ok {
		return EffectUnspecified
	}
	return NormalizeEffectClass(declarer.EffectClass())
}
