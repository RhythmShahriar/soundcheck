package interact

import (
	"fmt"
	"slices"
	"strings"

	"github.com/RhythmShahriar/soundcheck/check"
	"github.com/RhythmShahriar/soundcheck/model"
)

// issueSet merges issues with the same check and title found on one page,
// so six identical "Add to wishlist" buttons that all throw give one
// issue listing their selectors, not six.
type issueSet struct {
	order []string
	by    map[string]*grouped
}

type grouped struct {
	issue model.Issue
	sels  []string
}

func (s *issueSet) add(i model.Issue, sel string) {
	k := i.Check + "|" + i.Title
	if s.by == nil {
		s.by = map[string]*grouped{}
	}
	g, ok := s.by[k]
	if !ok {
		g = &grouped{issue: i}
		s.by[k] = g
		s.order = append(s.order, k)
	}
	if sel != "" && !slices.Contains(g.sels, sel) {
		g.sels = append(g.sels, sel)
	}
}

// flush adds the merged issues to p, with the selectors in Detail.
func (s *issueSet) flush(p *model.Page) {
	for _, k := range s.order {
		g := s.by[k]
		i := g.issue
		switch len(g.sels) {
		case 0:
		case 1:
			i.Detail = strings.TrimSpace(i.Detail + " (" + g.sels[0] + ")")
		default:
			slices.Sort(g.sels)
			i.Detail = strings.TrimSpace(fmt.Sprintf("%s (%d controls: %s)", i.Detail, len(g.sels), strings.Join(limit(g.sels, 10), ", ")))
		}
		check.Page(p, i)
	}
}
