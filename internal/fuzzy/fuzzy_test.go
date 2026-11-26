package fuzzy

import (
	"reflect"
	"sort"
	"testing"
)

func TestMatchBasics(t *testing.T) {
	cases := []struct {
		pat, text string
		ok        bool
		pos       []int
	}{
		{"", "anything", true, nil},
		{"abc", "abc", true, []int{0, 1, 2}},
		{"ABC", "abc", true, []int{0, 1, 2}},
		{"abc", "ABC", true, []int{0, 1, 2}},
		{"ac", "abc", true, []int{0, 2}},
		{"ca", "abc", false, nil},
		{"abcd", "abc", false, nil},
		{"x", "abc", false, nil},
		// Prefers the word start over the first occurrence.
		{"api", "my-app-api", true, []int{7, 8, 9}},
		// Prefers the consecutive run.
		{"ml", "mail-ml", true, []int{5, 6}},
		// camelCase humps.
		{"mA", "myApp", true, []int{0, 2}},
		// Unicode is matched per rune, not per byte.
		{"éa", "café-app", true, []int{3, 5}},
	}
	for _, c := range cases {
		_, pos, ok := Match(c.pat, c.text)
		if ok != c.ok || !reflect.DeepEqual(pos, c.pos) {
			t.Errorf("Match(%q, %q) = %v %v, want %v %v", c.pat, c.text, pos, ok, c.pos, c.ok)
		}
	}
}

func score(t *testing.T, pat, text string) int {
	t.Helper()
	s, _, ok := Match(pat, text)
	if !ok {
		t.Fatalf("%q should match %q", pat, text)
	}
	return s
}

func TestRanking(t *testing.T) {
	// Each pair: the first text should score higher than the second.
	better := [][3]string{
		{"api", "api", "api-gateway"},         // shorter exact
		{"api", "api-gateway", "my-api"},      // prefix over later word
		{"api", "my-api", "rapid"},            // word start over mid-word
		{"anl", "anycli-launcher", "ansible"}, // word starts over a scatter
		{"ag", "api-gateway", "language"},     // both word starts
	}
	for _, b := range better {
		if hi, lo := score(t, b[0], b[1]), score(t, b[0], b[2]); hi <= lo {
			t.Errorf("%q: %q (%d) should beat %q (%d)", b[0], b[1], hi, b[2], lo)
		}
	}
	// A contiguous match beats a scattered one even when it starts later.
	if hi, lo := score(t, "acl", "zz-acl-zz"), score(t, "acl", "anycli-launcher"); hi <= lo {
		t.Errorf("contiguous acl (%d) should beat scattered (%d)", hi, lo)
	}
}

func TestSortStability(t *testing.T) {
	names := []string{"gamma", "alpha", "work/api", "api", "delta", "rapid"}
	type r struct {
		name  string
		score int
	}
	var got []r
	for _, n := range names {
		if s, _, ok := Match("a", n); ok {
			got = append(got, r{n, s})
		}
	}
	sort.SliceStable(got, func(i, j int) bool { return got[i].score > got[j].score })
	// Word starts first (shorter first), then mid-word matches by position;
	// equal scores keep their input order.
	want := []string{"api", "alpha", "work/api", "gamma", "rapid", "delta"}
	var order []string
	for _, x := range got {
		order = append(order, x.name)
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("got %v want %v", order, want)
	}
}
