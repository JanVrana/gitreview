package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
)

// NewToken returns a random access token (128 bits, hex encoded).
func NewToken() string {
	b := make([]byte, 16)
	rand.Read(b) // never fails (crypto/rand panics on failure)
	return hex.EncodeToString(b)
}

// tokenAuth checks the access token. A valid ?token= query parameter is
// exchanged for an HttpOnly cookie and, for page loads, the browser is
// redirected to the same URL without the token, so it does not linger in the
// address bar or history. The cookie name is derived from the token, so
// several instances on one host (different ports share cookies) don't clash.
type tokenAuth struct {
	token  []byte
	cookie string
}

func newTokenAuth(token string) *tokenAuth {
	if token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	return &tokenAuth{token: []byte(token), cookie: "gitreview_" + hex.EncodeToString(sum[:4])}
}

func (a *tokenAuth) valid(s string) bool {
	return subtle.ConstantTimeCompare([]byte(s), a.token) == 1
}

// check reports whether the request may continue; otherwise it has already
// written the response (redirect or 401).
func (a *tokenAuth) check(w http.ResponseWriter, r *http.Request) bool {
	if q := r.URL.Query(); q.Has("token") {
		if !a.valid(q.Get("token")) {
			unauthorized(w)
			return false
		}
		http.SetCookie(w, &http.Cookie{
			Name: a.cookie, Value: string(a.token), Path: "/",
			HttpOnly: true, SameSite: http.SameSiteStrictMode,
		})
		if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
			q.Del("token")
			u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
			http.Redirect(w, r, u.String(), http.StatusSeeOther)
			return false
		}
		return true
	}
	if c, err := r.Cookie(a.cookie); err == nil && a.valid(c.Value) {
		return true
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") && a.valid(strings.TrimPrefix(h, "Bearer ")) {
		return true
	}
	unauthorized(w)
	return false
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("gitreview: missing or invalid access token.\n\n" +
		"Open the URL printed in the terminal where gitreview runs (it ends with ?token=…).\n" +
		"Otevři URL vypsanou v terminálu, kde gitreview běží (končí na ?token=…).\n"))
}
