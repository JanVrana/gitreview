package server

import (
	"net/http"

	"github.com/JanVrana/gitreview/internal/git"
)

// log lists the commits of the compared range (left..right, HEAD for the
// working tree), newest first; one of them can then be shown with commit=.
func (s *Server) log(r *http.Request) (any, error) {
	c, err := s.resolveRange(r)
	if err != nil {
		return nil, err
	}
	resp := map[string]any{"commits": []git.Commit{}, "more": false}
	if c.Stash != nil {
		return resp, nil
	}
	to := c.Right.Rev
	if to == "" {
		to = s.repo.Head().Sha
	}
	commits, more, err := s.repo.Log(c.Left.Rev, to)
	if err != nil {
		return nil, err
	}
	resp["commits"], resp["more"] = commits, more
	return resp, nil
}
