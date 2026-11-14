package db

import (
	"slices"
	"testing"
)

func TestNormalizeSeasons(t *testing.T) {
	if got := NormalizeSeasons([]int{3, 1, 3, -1, 0, 1999}); !slices.Equal(got, []int{0, 1, 3}) {
		t.Errorf("NormalizeSeasons = %v, want [0 1 3]", got)
	}
	if got := NormalizeSeasons(nil); got == nil || len(got) != 0 {
		t.Errorf("NormalizeSeasons(nil) = %#v, want an empty slice", got)
	}
}

func TestDisjointSeasons(t *testing.T) {
	tests := []struct {
		a, b []int
		want bool
	}{
		{[]int{1}, []int{2, 3}, true},
		{[]int{2, 3}, []int{3}, false},
		{[]int{1}, nil, false},
		{nil, nil, false},
	}
	for _, tt := range tests {
		if got := disjointSeasons(tt.a, tt.b); got != tt.want {
			t.Errorf("disjointSeasons(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
