// Package server exposes the review API and the embedded web UI.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gitreview/internal/diff"
	"gitreview/internal/git"
	"gitreview/internal/lang"
	"gitreview/internal/review"
)

const maxLines = 20000 // larger files need explicit "show anyway"

// Server serves one repository.
type Server struct {
	repo       *git.Repo
	store      *review.Store
	web        fs.FS
	allowHosts map[string]bool
	watch      *watcher
}

// New creates a server for repo; web holds index.html and assets.
// allowHosts lists extra host names (besides IP literals, localhost and the
// machine's hostname) the UI may be reached by.
func New(repo *git.Repo, web fs.FS, allowHosts []string) (*Server, error) {
	dir, err := repo.GitPath("gitreview")
	if err != nil {
		return nil, err
	}
	hosts := map[string]bool{"localhost": true}
	for _, h := range allowHosts {
		hosts[strings.ToLower(h)] = true
	}
	return &Server{repo: repo, store: review.NewStore(dir), web: web, allowHosts: hosts, watch: newWatcher(repo)}, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /api/info", api(s.info))
	mux.Handle("GET /api/files", api(s.files))
	mux.Handle("GET /api/diff", api(s.diff))
	mux.HandleFunc("GET /api/events", s.events)
	mux.Handle("GET /api/comments", api(s.commentList))
	mux.Handle("POST /api/comments", api(s.commentAdd))
	mux.Handle("PUT /api/comments", api(s.commentUpdate))
	mux.Handle("DELETE /api/comments", api(s.commentDelete))
	mux.Handle("POST /api/export", api(s.export))
	static := http.FileServerFS(s.web)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	return s.guard(mux)
}

// guard protects the unauthenticated server against other web pages open in
// the user's browser:
//   - DNS rebinding: only IP literals, localhost and known host names are
//     accepted in the Host header.
//   - Cross-site writes: state-changing requests must be JSON (which forces a
//     CORS preflight the server never grants) and, when the browser sends an
//     Origin, it must be this server.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if !s.hostAllowed(r.Host) {
			http.Error(w, "nepovolený Host – přidej ho přes -allow-host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			ct := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
			if !strings.EqualFold(ct, "application/json") {
				http.Error(w, "vyžadován Content-Type: application/json", http.StatusUnsupportedMediaType)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "cizí Origin", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if net.ParseIP(host) != nil || s.allowHosts[host] {
		return true
	}
	return false
}

// ---- JSON plumbing ------------------------------------------------------------

// httpError carries an HTTP status with an error message for the UI.
type httpError struct {
	code int
	err  error
}

func (e httpError) Error() string { return e.err.Error() }

func badRequest(err error) error { return httpError{http.StatusBadRequest, err} }

// api adapts a handler returning (value, error) to http.Handler. Errors
// become {"error": "..."} with the status from httpError (default 500).
func api(fn func(*http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			code := http.StatusInternalServerError
			var he httpError
			if errors.As(err, &he) {
				code = he.code
			}
			w.WriteHeader(code)
			v = map[string]string{"error": err.Error()}
		}
		if err := json.NewEncoder(w).Encode(v); err != nil {
			log.Printf("encode: %v", err)
		}
	})
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return badRequest(err)
	}
	return nil
}

// ---- compare resolution ---------------------------------------------------------

// side describes one side of the comparison.
type side struct {
	Rev   string `json:"rev"` // commit sha, "" = working tree
	Label string `json:"label"`
}

type compare struct {
	Base    string `json:"base"`   // ref actually used, e.g. origin/develop
	OnBase  bool   `json:"onBase"` // HEAD is the base commit
	Left    side   `json:"left"`
	Right   side   `json:"right"`
	IgnoreW bool   `json:"ignoreWhitespace"`
}

func (s *Server) resolve(r *http.Request) (*compare, error) {
	q := r.URL.Query()
	baseName := q.Get("base")
	if baseName == "" {
		baseName = "develop"
	}
	base, err := s.repo.ResolveBase(baseName)
	if err != nil {
		return nil, badRequest(err)
	}
	baseSha, err := s.repo.Verify(base)
	if err != nil {
		return nil, badRequest(err)
	}
	head := s.repo.Head()
	if head.Sha == "" {
		return nil, badRequest(errors.New("repozitář nemá žádný commit"))
	}
	c := &compare{Base: base, IgnoreW: q.Get("ws") == "1", OnBase: baseSha == head.Sha}

	if q.Get("left") == "tip" {
		c.Left = side{Rev: baseSha, Label: fmt.Sprintf("%s @ %s", base, git.ShortSha(baseSha))}
	} else {
		mb, err := s.repo.MergeBase(baseSha, head.Sha)
		if err != nil {
			return nil, badRequest(err)
		}
		c.Left = side{Rev: mb, Label: fmt.Sprintf("merge-base(%s) @ %s", base, git.ShortSha(mb))}
	}
	headName := head.Branch
	if headName == "" {
		headName = "HEAD"
	}
	if q.Get("right") == "head" {
		c.Right = side{Rev: head.Sha, Label: fmt.Sprintf("%s @ %s", headName, git.ShortSha(head.Sha))}
	} else {
		c.Right = side{Label: headName + " – pracovní kopie"}
	}
	return c, nil
}

// changed resolves the comparison and lists its changed files. Our own
// REVIEW.md is never part of the list (in case it is not git-ignored).
func (s *Server) changed(r *http.Request) (*compare, []git.FileChange, error) {
	c, err := s.resolve(r)
	if err != nil {
		return nil, nil, err
	}
	files, err := s.repo.ChangedFiles(c.Left.Rev, c.Right.Rev)
	if err != nil {
		return nil, nil, err
	}
	out := make([]git.FileChange, 0, len(files))
	for _, f := range files {
		if f.Status != "U" || f.Path != review.ExportFile {
			out = append(out, f)
		}
	}
	return c, out, nil
}

// ---- handlers ----------------------------------------------------------------------

type baseInfo struct {
	Name   string `json:"name"`
	Ref    string `json:"ref,omitempty"`
	Exists bool   `json:"exists"`
}

func (s *Server) info(*http.Request) (any, error) {
	var bases []baseInfo
	for _, n := range []string{"develop", "master", "main"} {
		ref, err := s.repo.ResolveBase(n)
		bases = append(bases, baseInfo{Name: n, Ref: ref, Exists: err == nil})
	}
	return map[string]any{
		"root":     s.repo.Root,
		"name":     filepath.Base(s.repo.Root),
		"head":     s.repo.Head(),
		"bases":    bases,
		"branches": s.repo.Branches(),
	}, nil
}

func (s *Server) files(r *http.Request) (any, error) {
	c, files, err := s.changed(r)
	if err != nil {
		return nil, err
	}
	return map[string]any{"compare": c, "files": files}, nil
}

type diffSide struct {
	side
	Lines  []string `json:"lines"`
	Exists bool     `json:"exists"`
}

// content loads one side of a file; a missing file is not an error.
func (s *Server) content(sd side, path string, wanted bool) (diffSide, []byte, error) {
	ds := diffSide{side: sd, Lines: []string{}}
	if !wanted {
		return ds, nil, nil
	}
	data, err := s.repo.Content(sd.Rev, path)
	if errors.Is(err, git.ErrNotExist) {
		return ds, nil, nil
	}
	if err != nil {
		return ds, nil, err
	}
	ds.Exists = true
	return ds, data, nil
}

func (s *Server) diff(r *http.Request) (any, error) {
	c, files, err := s.changed(r)
	if err != nil {
		return nil, err
	}
	path := r.URL.Query().Get("path")
	// Only files that are part of the comparison may be read.
	var fc *git.FileChange
	for i := range files {
		if files[i].Path == path {
			fc = &files[i]
			break
		}
	}
	if fc == nil {
		return nil, httpError{http.StatusNotFound, fmt.Errorf("soubor %q není mezi změněnými", path)}
	}
	resp := map[string]any{"file": fc, "compare": c, "lang": lang.ForPath(fc.Path)}
	if fc.Binary {
		resp["binary"] = true
		return resp, nil
	}

	oldPath := fc.Path
	if fc.OldPath != "" {
		oldPath = fc.OldPath
	}
	left, leftData, err := s.content(c.Left, oldPath, fc.Status != "A" && fc.Status != "U")
	if err != nil {
		return nil, err
	}
	right, rightData, err := s.content(c.Right, fc.Path, fc.Status != "D")
	if err != nil {
		return nil, err
	}
	if git.IsBinary(leftData) || git.IsBinary(rightData) {
		resp["binary"] = true
		return resp, nil
	}
	left.Lines, right.Lines = git.SplitLines(leftData), git.SplitLines(rightData)

	tooLarge := len(leftData) > git.MaxTextBytes || len(rightData) > git.MaxTextBytes ||
		len(left.Lines) > maxLines || len(right.Lines) > maxLines
	if tooLarge && r.URL.Query().Get("force") != "1" {
		resp["tooLarge"] = true
		resp["size"] = max(len(leftData), len(rightData))
		return resp, nil
	}

	var blocks []diff.Block
	if !left.Exists || !right.Exists {
		blocks = diff.Whole(len(left.Lines), len(right.Lines))
	} else {
		out, err := s.repo.Hunks(c.Left.Rev, c.Right.Rev, fc.OldPath, fc.Path, c.IgnoreW)
		if err != nil {
			return nil, err
		}
		blocks = diff.Blocks(diff.ParseHunks(out), len(left.Lines), len(right.Lines))
	}
	resp["left"], resp["right"], resp["blocks"] = left, right, blocks
	return resp, nil
}

// events streams a "changed" event whenever the repository changes.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming nepodporován", http.StatusInternalServerError)
		return
	}
	ch, unsubscribe := s.watch.subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, "retry: 2000\nevent: hello\ndata: {}\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
		case fp := <-ch:
			fmt.Fprintf(w, "event: changed\ndata: %s\n\n", fp)
		}
		flusher.Flush()
	}
}

func (s *Server) commentList(*http.Request) (any, error) {
	return s.store.List()
}

func (s *Server) commentAdd(r *http.Request) (any, error) {
	var c review.Comment
	if err := decode(r, &c); err != nil {
		return nil, err
	}
	saved, err := s.store.Add(c)
	if err != nil {
		return nil, badRequest(err)
	}
	return saved, nil
}

func (s *Server) commentUpdate(r *http.Request) (any, error) {
	var body struct{ ID, Text string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	saved, err := s.store.Update(body.ID, body.Text)
	if err != nil {
		return nil, badRequest(err)
	}
	return saved, nil
}

func (s *Server) commentDelete(r *http.Request) (any, error) {
	q := r.URL.Query()
	var err error
	if q.Get("all") == "1" {
		err = s.store.Clear()
	} else {
		err = s.store.Delete(q.Get("id"))
	}
	return map[string]bool{"ok": err == nil}, err
}

func (s *Server) export(r *http.Request) (any, error) {
	var body struct {
		Left, Right string
		Write       bool
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	cs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	md := review.Markdown(cs, review.ExportInfo{
		Branch: s.repo.Head().Branch, LeftDesc: oneLine(body.Left), RightDesc: oneLine(body.Right),
	})
	resp := map[string]any{"markdown": md, "count": len(cs)}
	if body.Write {
		exclude, err := s.repo.GitPath("info/exclude")
		if err != nil {
			return nil, err
		}
		path, err := review.WriteExport(s.repo.Root, exclude, md)
		if err != nil {
			return nil, err
		}
		resp["path"] = path
	}
	return resp, nil
}

// oneLine flattens a client-supplied label for the Markdown header and caps
// it at 200 bytes without splitting a UTF-8 sequence.
func oneLine(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	if len(s) <= 200 {
		return s
	}
	cut := 200
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
