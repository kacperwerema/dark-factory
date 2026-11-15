package outputs

import (
	"strings"
	"testing"
)

func TestApprovedError(t *testing.T) {
	out := func(id, kind, state string, versions int) *Output {
		o := &Output{ID: id, Issue: "m/f", Kind: kind, State: state}
		for i := 1; i <= versions; i++ {
			o.Versions = append(o.Versions, Version{N: i})
		}
		return o
	}
	cases := []struct {
		name string
		list []*Output
		kind string
		want string
	}{
		{"none", nil, "report", "no report on the shelf"},
		{"approved", []*Output{out("a", "report", StateApproved, 1)}, "report", ""},
		{"published", []*Output{out("a", "report", StatePublished, 1)}, "", ""},
		{"newest in review", []*Output{out("a", "report", StateApproved, 1), out("b", "report", StateReview, 1)}, "report", "b v1 still in review"},
		{"revision in review", []*Output{out("a", "report", StateReview, 3)}, "report", "a v3 still in review"},
		{"other kind in review", []*Output{out("a", "report", StateApproved, 1), out("b", "post", StateReview, 1)}, "report", ""},
		{"only rejected", []*Output{out("a", "report", StateRejected, 1)}, "report", "every one is rejected"},
		{"rejected beside approved", []*Output{out("a", "report", StateApproved, 1), out("b", "report", StateRejected, 1)}, "report", ""},
	}
	for _, c := range cases {
		err := ApprovedError(c.list, "m/f", c.kind)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
