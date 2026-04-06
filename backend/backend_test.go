package backend

import "testing"

func TestMatchSetGlob(t *testing.T) {
	tests := []struct {
		patterns MatchSet
		input    string
		want     bool
	}{
		{MatchSet{"*[bot]"}, "dependabot[bot]", true},
		{MatchSet{"*[bot]"}, "github-actions[bot]", true},
		{MatchSet{"*[bot]"}, "qodo-code-review[bot]", true},
		{MatchSet{"*[bot]"}, "realuser", false},
		{MatchSet{"*[bot]"}, "bot", false},
		{MatchSet{"exact-user"}, "exact-user", true},
		{MatchSet{"exact-user"}, "other-user", false},
		{MatchSet{"pre*"}, "prefix-match", true},
		{MatchSet{"pre*"}, "nope", false},
		{MatchSet{"*mid*"}, "has-mid-dle", true},
		{MatchSet{"*mid*"}, "nope", false},
		{MatchSet{"a*b*c"}, "aXbYc", true},
		{MatchSet{"a*b*c"}, "abc", true},
		{MatchSet{"a*b*c"}, "aXc", false},
		{nil, "anything", false},
		{MatchSet{}, "anything", false},
		{MatchSet{"*"}, "anything", true},
		{MatchSet{"one", "*[bot]"}, "dependabot[bot]", true},
		{MatchSet{"one", "*[bot]"}, "one", true},
		{MatchSet{"one", "*[bot]"}, "two", false},
	}
	for _, tt := range tests {
		got := tt.patterns.Match(tt.input)
		if got != tt.want {
			t.Errorf("MatchSet%v.Match(%q) = %v, want %v", []string(tt.patterns), tt.input, got, tt.want)
		}
	}
}
