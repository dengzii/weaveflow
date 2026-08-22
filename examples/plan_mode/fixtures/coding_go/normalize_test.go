package codinggo

import (
	"reflect"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "nil", input: nil, want: []string{}},
		{name: "trim lowercase and deduplicate", input: []string{" Go ", "RUNTIME", "go", "", " runtime "}, want: []string{"go", "runtime"}},
		{name: "preserve first normalized order", input: []string{"Beta", "alpha", "BETA", "Gamma"}, want: []string{"beta", "alpha", "gamma"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := append([]string(nil), test.input...)
			got := NormalizeTags(input)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("NormalizeTags(%#v) = %#v, want %#v", test.input, got, test.want)
			}
			if !reflect.DeepEqual(input, test.input) {
				t.Fatalf("NormalizeTags mutated input: got %#v, want %#v", input, test.input)
			}
		})
	}
}
