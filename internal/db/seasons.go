package db

import "slices"

// MaxSeason bounds a season number, so a year or a typo is not taken for one.
const MaxSeason = 100

// NormalizeSeasons sorts season numbers and drops duplicates and numbers out
// of range (0 is the specials). It never returns nil.
func NormalizeSeasons(seasons []int) []int {
	out := slices.DeleteFunc(slices.Clone(seasons), func(n int) bool { return n < 0 || n > MaxSeason })
	slices.Sort(out)
	out = slices.Compact(out)
	if out == nil {
		out = []int{}
	}
	return out
}

// disjointSeasons says that two items hold different seasons of a series:
// both say which, and none is on both.
func disjointSeasons(a, b []int) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for _, n := range a {
		if slices.Contains(b, n) {
			return false
		}
	}
	return true
}
