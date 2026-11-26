// Package fuzzy is a small case-insensitive subsequence matcher with
// fzf-like scoring: every pattern character must appear in order, and the
// best alignment is preferred (consecutive runs, word starts, early
// matches). Labels are short, so an exact O(n*m^2) search is cheap.
package fuzzy

import (
	"unicode"
)

const (
	scoreMatch       = 16
	bonusStart       = 12 // first character of the text
	bonusBoundary    = 8  // after a separator, or a lower->Upper camel hump
	bonusConsecutive = 8
	penaltyGap       = 1 // per skipped character between matches
	penaltyLeading   = 1 // per character before the first match, capped
	maxLeading       = 6
)

// Match reports whether pattern is a subsequence of text (ignoring case)
// and, if so, its score (higher is better) and the matched rune indexes in
// text. An empty pattern matches everything with score 0.
func Match(pattern, text string) (score int, positions []int, ok bool) {
	p := []rune(pattern)
	t := []rune(text)
	if len(p) == 0 {
		return 0, nil, true
	}
	if len(p) > len(t) {
		return 0, nil, false
	}
	lp := make([]rune, len(p))
	for i, r := range p {
		lp[i] = unicode.ToLower(r)
	}
	lt := make([]rune, len(t))
	for i, r := range t {
		lt[i] = unicode.ToLower(r)
	}

	const none = -1 << 30
	n, m := len(p), len(t)
	// best[i][j]: best score with pattern[i] matched at text[j].
	best := make([][]int, n)
	from := make([][]int, n)
	for i := range best {
		best[i] = make([]int, m)
		from[i] = make([]int, m)
		for j := range best[i] {
			best[i][j] = none
			from[i][j] = -1
		}
	}
	for j := 0; j < m; j++ {
		if lt[j] != lp[0] {
			continue
		}
		lead := j
		if lead > maxLeading {
			lead = maxLeading
		}
		best[0][j] = scoreMatch + bonus(t, j) - lead*penaltyLeading
	}
	for i := 1; i < n; i++ {
		for j := i; j < m; j++ {
			if lt[j] != lp[i] {
				continue
			}
			for k := i - 1; k < j; k++ {
				if best[i-1][k] == none {
					continue
				}
				s := best[i-1][k] + scoreMatch + bonus(t, j)
				if k == j-1 {
					s += bonusConsecutive
				} else {
					s -= (j - k - 1) * penaltyGap
				}
				if s > best[i][j] {
					best[i][j], from[i][j] = s, k
				}
			}
		}
	}
	end, top := -1, none
	for j := 0; j < m; j++ {
		if best[n-1][j] > top {
			end, top = j, best[n-1][j]
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	positions = make([]int, n)
	for i, j := n-1, end; i >= 0; i-- {
		positions[i] = j
		j = from[i][j]
	}
	// Shorter texts win ties: "api" over "api-gateway" for "api".
	top -= (m - n) / 4
	return top, positions, true
}

func bonus(t []rune, j int) int {
	if j == 0 {
		return bonusStart
	}
	prev, cur := t[j-1], t[j]
	switch {
	case isSep(prev):
		return bonusBoundary
	case unicode.IsLower(prev) && unicode.IsUpper(cur):
		return bonusBoundary
	case unicode.IsLetter(prev) && unicode.IsDigit(cur):
		return bonusBoundary / 2
	}
	return 0
}

func isSep(r rune) bool {
	switch r {
	case '/', '\\', '-', '_', '.', ' ', ':':
		return true
	}
	return false
}
