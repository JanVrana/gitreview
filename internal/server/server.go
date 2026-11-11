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
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/JanVrana/gitreview/internal/diff"
	"github.com/JanVrana/gitreview/internal/git"
	"github.com/JanVrana/gitreview/internal/lang"
	"github.com/JanVrana/gitreview/internal/review"
)

const maxLines = 20000 // larger files need explicit "show anyway"

// Server serves one repository.
type Server struct {
	repo       *git.Repo
	store      *review.Store
	web        fs.FS
	allowHosts map[string]bool
	auth       *tokenAuth // nil = no authentication
	version    string
	watch      *watcher
	writing    sync.Mutex // one git write (fetch, commit, push, …) at a time
	updater    Updater    // nil = no manual updates
	updating   sync.Mutex
	lastRevert *blockUndo // guarded by writing
}

// Options configure a Server.
type Options struct {
	// AllowHosts lists extra host names (besides IP literals, localhost and
	// the machine's hostname) the UI may be reached by.
	AllowHosts []string
	// Token is the access token required on every request; "" disables
	// authentication.
	Token string
	// Version is shown in the UI; a change seen after reconnecting makes
	// open pages reload.
	Version string
	// Updater enables checking for and installing updates from the UI.
	Updater Updater
}

// New creates a server for repo; web holds index.html and assets.
func New(repo *git.Repo, web fs.FS, opts Options) (*Server, error) {
	dir, err := repo.GitPath("gitreview")
	if err != nil {
		return nil, err
	}
	hosts := map[string]bool{"localhost": true}
	for _, h := range opts.AllowHosts {
		hosts[strings.ToLower(h)] = true
	}
	return &Server{repo: repo, store: review.NewStore(dir), web: web, allowHosts: hosts, auth: newTokenAuth(opts.Token), version: opts.Version, updater: opts.Updater, watch: newWatcher(repo)}, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /api/info", api(s.info))
	mux.Handle("GET /api/files", api(s.files))
	mux.Handle("GET /api/diff", api(s.diff))
	mux.Handle("GET /api/blame", api(s.blame))
	mux.HandleFunc("GET /api/events", s.events)
	mux.Handle("GET /api/comments", api(s.commentList))
	mux.Handle("POST /api/comments", api(s.commentAdd))
	mux.Handle("PUT /api/comments", api(s.commentUpdate))
	mux.Handle("DELETE /api/comments", api(s.commentDelete))
	mux.Handle("POST /api/export", api(s.export))
	mux.Handle("POST /api/fetch", api(s.fetch))
	mux.Handle("GET /api/status", api(s.status))
	mux.Handle("POST /api/commit", api(s.commit))
	mux.Handle("POST /api/revert", api(s.revert))
	mux.Handle("POST /api/revert-block", api(s.revertBlock))
	mux.Handle("POST /api/revert-block/undo", api(s.undoRevertBlock))
	mux.Handle("POST /api/push", api(s.push))
	mux.Handle("POST /api/pull", api(s.pull))
	mux.Handle("POST /api/stash", api(s.stashAction))
	mux.Handle("GET /api/update", api(s.updateCheck))
	mux.Handle("POST /api/update", api(s.updateNow))
	static := http.FileServerFS(s.web)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	return s.guard(mux)
}

// guard protects the server against other web pages open in the user's
// browser and, unless disabled, requires the access token:
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
			http.Error(w, "host not allowed – add it with -allow-host", http.StatusForbidden)
			return
		}
		if s.auth != nil && !s.auth.check(w, r) {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			ct := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
			if !strings.EqualFold(ct, "application/json") {
				http.Error(w, "Content-Type: application/json required", http.StatusUnsupportedMediaType)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || !strings.EqualFold(u.Host, r.Host) {
					http.Error(w, "cross-origin request rejected", http.StatusForbidden)
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
	Base    string     `json:"base"`   // ref actually used, e.g. origin/develop
	OnBase  bool       `json:"onBase"` // HEAD is the base commit
	Left    side       `json:"left"`
	Right   side       `json:"right"`
	IgnoreW bool       `json:"ignoreWhitespace"`
	Stash   *git.Stash `json:"stash,omitempty"` // browsing a stash instead of the branch
	// RightBranch: the right side is another branch, not the checked-out one.
	RightBranch bool `json:"rightBranch,omitempty"`
}

func (s *Server) resolve(r *http.Request) (*compare, error) {
	q := r.URL.Query()
	if sha := q.Get("stash"); sha != "" {
		return s.resolveStash(sha, q.Get("ws") == "1")
	}
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
		return nil, badRequest(errors.New("the repository has no commits"))
	}
	// The right side is the working tree, HEAD, or any branch ("branch:<name>",
	// compared without checking it out).
	rightName, rightSha := "", head.Sha
	if name, ok := strings.CutPrefix(q.Get("right"), "branch:"); ok {
		if !git.ValidRef(name) || !slices.Contains(s.repo.Branches(), name) {
			return nil, badRequest(fmt.Errorf("branch %q does not exist", name))
		}
		if rightSha, err = s.repo.Verify(name); err != nil {
			return nil, badRequest(err)
		}
		rightName = name
	}
	c := &compare{Base: base, IgnoreW: q.Get("ws") == "1", OnBase: baseSha == rightSha, RightBranch: rightName != ""}

	if q.Get("left") == "tip" {
		c.Left = side{Rev: baseSha, Label: fmt.Sprintf("%s @ %s", base, git.ShortSha(baseSha))}
	} else {
		mb, err := s.repo.MergeBase(baseSha, rightSha)
		if err != nil {
			return nil, badRequest(err)
		}
		c.Left = side{Rev: mb, Label: fmt.Sprintf("merge-base(%s) @ %s", base, git.ShortSha(mb))}
	}
	headName := head.Branch
	if headName == "" {
		headName = "HEAD"
	}
	if rightName != "" {
		c.Right = side{Rev: rightSha, Label: fmt.Sprintf("%s @ %s", rightName, git.ShortSha(rightSha))}
	} else if q.Get("right") == "head" {
		c.Right = side{Rev: head.Sha, Label: fmt.Sprintf("%s @ %s", headName, git.ShortSha(head.Sha))}
	} else {
		c.Right = side{Label: headName} // the UI appends a translated "working tree"
	}
	return c, nil
}

// resolveStash compares a stash with the commit it was made on.
func (s *Server) resolveStash(sha string, ws bool) (*compare, error) {
	if !shaRe.MatchString(sha) {
		return nil, badRequest(errors.New("invalid stash"))
	}
	st, err := s.repo.FindStash(sha)
	if err != nil {
		return nil, httpError{http.StatusNotFound, err}
	}
	parent, err := s.repo.Verify(sha + "^1")
	if err != nil {
		return nil, err
	}
	return &compare{
		Base:    st.Ref,
		IgnoreW: ws,
		Stash:   &st,
		Left:    side{Rev: parent, Label: fmt.Sprintf("%s base @ %s", st.Ref, git.ShortSha(parent))},
		Right:   side{Rev: sha, Label: fmt.Sprintf("%s: %s", st.Ref, st.Message)},
	}, nil
}

// changed resolves the comparison and lists its changed files. Our own
// REVIEW.md is never part of the list (in case it is not git-ignored).
func (s *Server) changed(r *http.Request) (*compare, []git.FileChange, error) {
	c, err := s.resolve(r)
	if err != nil {
		return nil, nil, err
	}
	if c.Stash != nil {
		files, err := s.repo.StashFiles(*c.Stash)
		return c, files, err
	}
	files, err := s.repo.ChangedFiles(c.Left.Rev, c.Right.Rev)
	if err != nil {
		return nil, nil, err
	}
	pending := map[string]git.Pending{}
	if !c.RightBranch { // uncommitted changes belong to the checked-out branch
		pending, _ = s.repo.Uncommitted()
	}
	out := make([]git.FileChange, 0, len(files))
	for _, f := range files {
		if f.Status != "U" || f.Path != review.ExportFile {
			_, f.Uncommitted = pending[f.Path]
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
	// Local branch compared with its remote-tracking branch as of the last
	// fetch (no network access here).
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
}

// baseNames are the branches offered as comparison bases and kept up to
// date by fetch.
var baseNames = []string{"develop", "master", "main"}

func (s *Server) info(*http.Request) (any, error) {
	var bases []baseInfo
	for _, n := range baseNames {
		ref, err := s.repo.ResolveBase(n)
		b := baseInfo{Name: n, Ref: ref, Exists: err == nil}
		if b.Exists && ref == n { // a local branch: how far is it from the remote?
			if b.Upstream = s.repo.Upstream(n); b.Upstream != "" {
				b.Ahead, b.Behind, _ = s.repo.AheadBehind(n, b.Upstream)
			}
		}
		bases = append(bases, b)
	}
	local, remote := s.repo.BranchesSplit()
	head := s.repo.Head()
	branch := map[string]any{}
	if head.Branch != "" {
		if up := s.repo.Upstream(head.Branch); up != "" {
			a, b, _ := s.repo.AheadBehind(head.Branch, up)
			branch = map[string]any{"upstream": up, "ahead": a, "behind": b}
		}
	}
	return map[string]any{
		"version":  s.version,
		"tracking": branch,
		"stashes":  s.stashes(),
		"root":     s.repo.Root,
		"name":     filepath.Base(s.repo.Root),
		"head":     head,
		"bases":    bases,
		"branches": append(slices.Clone(local), remote...),
		"local":    local,
		"remote":   remote,
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
	// Hash of the working-tree file when single blocks can be reverted;
	// the revert is refused if the file changed since.
	Hash string `json:"hash,omitempty"`
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

// fileDiff is one file of the comparison with both sides loaded.
type fileDiff struct {
	c                   *compare
	fc                  *git.FileChange
	left, right         diffSide
	leftData, rightData []byte
	binary, tooLarge    bool
	blocks              []diff.Block
}

// loadDiff loads a changed file and, unless it is binary or too large (and
// not forced), splits it into blocks.
func (s *Server) loadDiff(r *http.Request, path string, force bool) (*fileDiff, error) {
	c, files, err := s.changed(r)
	if err != nil {
		return nil, err
	}
	// Only files that are part of the comparison may be read.
	i := slices.IndexFunc(files, func(f git.FileChange) bool { return f.Path == path })
	if i < 0 {
		return nil, httpError{http.StatusNotFound, fmt.Errorf("file %q is not among the changed files", path)}
	}
	fd := &fileDiff{c: c, fc: &files[i]}
	fc := fd.fc
	if fc.Binary {
		fd.binary = true
		return fd, nil
	}
	oldPath := fc.Path
	if fc.OldPath != "" {
		oldPath = fc.OldPath
	}
	if fd.left, fd.leftData, err = s.content(c.Left, oldPath, fc.Status != "A" && fc.Status != "U"); err != nil {
		return nil, err
	}
	rightSide := c.Right
	if fc.Src != "" {
		rightSide.Rev = fc.Src
	}
	if fd.right, fd.rightData, err = s.content(rightSide, fc.Path, fc.Status != "D"); err != nil {
		return nil, err
	}
	if git.IsBinary(fd.leftData) || git.IsBinary(fd.rightData) {
		fd.binary = true
		return fd, nil
	}
	fd.left.Lines, fd.right.Lines = git.SplitLines(fd.leftData), git.SplitLines(fd.rightData)

	fd.tooLarge = len(fd.leftData) > git.MaxTextBytes || len(fd.rightData) > git.MaxTextBytes ||
		len(fd.left.Lines) > maxLines || len(fd.right.Lines) > maxLines
	if fd.tooLarge && !force {
		return fd, nil
	}
	if !fd.left.Exists || !fd.right.Exists {
		fd.blocks = diff.Whole(len(fd.left.Lines), len(fd.right.Lines))
	} else {
		out, err := s.repo.Hunks(c.Left.Rev, c.Right.Rev, fc.OldPath, fc.Path, c.IgnoreW)
		if err != nil {
			return nil, err
		}
		fd.blocks = diff.Blocks(diff.ParseHunks(out), len(fd.left.Lines), len(fd.right.Lines))
	}
	return fd, nil
}

// blockRevertible tells whether single blocks can be reverted: the right
// side is the working-tree file and the left side exists.
func (fd *fileDiff) blockRevertible() bool {
	return fd.c.Stash == nil && fd.c.Right.Rev == "" && fd.fc.Src == "" &&
		fd.left.Exists && fd.right.Exists && !fd.binary && fd.fc.Status != "U"
}

func (s *Server) diff(r *http.Request) (any, error) {
	q := r.URL.Query()
	fd, err := s.loadDiff(r, q.Get("path"), q.Get("force") == "1")
	if err != nil {
		return nil, err
	}
	resp := map[string]any{"file": fd.fc, "compare": fd.c, "lang": lang.ForPath(fd.fc.Path)}
	switch {
	case fd.binary:
		resp["binary"] = true
	case fd.blocks == nil:
		resp["tooLarge"] = true
		resp["size"] = max(len(fd.leftData), len(fd.rightData))
	default:
		if fd.blockRevertible() {
			fd.right.Hash = contentHash(fd.rightData)
		}
		resp["left"], resp["right"], resp["blocks"] = fd.left, fd.right, fd.blocks
	}
	return resp, nil
}

// events streams a "changed" event whenever the repository changes.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ch, unsubscribe := s.watch.subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	hello, _ := json.Marshal(map[string]string{"version": s.version})
	fmt.Fprintf(w, "retry: 2000\nevent: hello\ndata: %s\n\n", hello)
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
		Lang        string
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
		Branch: s.repo.Head().Branch, LeftDesc: oneLine(body.Left), RightDesc: oneLine(body.Right), Lang: body.Lang,
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

// fetch runs `git fetch --all --prune` and fast-forwards the local base
// branches (and the selected base, if it is another local branch) to their
// remote-tracking branches. Branches with local commits or checked out in a
// worktree are left alone and reported.
func (s *Server) fetch(r *http.Request) (any, error) {
	var body struct{ Base string }
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	unlock, err := s.lockWrite()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.repo.Fetch(); err != nil {
		return nil, httpError{http.StatusBadGateway, err}
	}
	names := baseNames
	if body.Base != "" && !slices.Contains(names, body.Base) {
		names = append(slices.Clone(names), body.Base)
	}
	updates := []git.BranchUpdate{}
	for _, n := range names {
		if !s.repo.LocalBranch(n) {
			continue // remote-only bases (origin/x) are already current after the fetch
		}
		u, err := s.repo.FastForward(n)
		if err != nil {
			return nil, err
		}
		updates = append(updates, u)
	}
	return map[string]any{"branches": updates}, nil
}

// PrepareRestart waits for a running git write to finish and blocks new ones, so
// the process can be replaced without interrupting a git write.
func (s *Server) PrepareRestart() {
	s.writing.Lock()
}

// AbortRestart undoes PrepareRestart when the restart failed.
func (s *Server) AbortRestart() {
	s.writing.Unlock()
}
