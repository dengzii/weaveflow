package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetryPolicyJSONPreservesExplicitEmptyErrorClassLists(t *testing.T) {
	data, err := json.Marshal(RetryPolicy{RetryableErrorClasses: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	if !strings.Contains(serialized, `"retryable_error_classes":[]`) || strings.Contains(serialized, "non_retryable_error_classes") {
		t.Fatalf("serialized retry policy = %s", serialized)
	}

	var decoded RetryPolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RetryableErrorClasses == nil || len(decoded.RetryableErrorClasses) != 0 {
		t.Fatalf("decoded retryable classes = %#v", decoded.RetryableErrorClasses)
	}
	if decoded.NonRetryableErrorClasses != nil {
		t.Fatalf("decoded non-retryable classes = %#v", decoded.NonRetryableErrorClasses)
	}
}
