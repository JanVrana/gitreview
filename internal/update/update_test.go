package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.4.0", "v0.3.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.3.0", "v0.3.0", false},
		{"v0.2.9", "v0.3.0", false},
		{"v0.4.0", "dev", false},
		{"v0.4.0", "v0.3.0-1-gabc-dirty", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// fakeGitHub serves /releases/latest and the assets of one release.
func fakeGitHub(t *testing.T, tag string, bin []byte, sums string) *Updater {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("HEAD /o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/o/r/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("GET /o/r/releases/download/"+tag+"/"+AssetName(), func(w http.ResponseWriter, _ *http.Request) { w.Write(bin) })
	mux.HandleFunc("GET /o/r/releases/download/"+tag+"/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(sums)) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &Updater{Repo: "o/r", BaseURL: ts.URL, Client: ts.Client()}
}

func sumLine(data []byte, name string) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:]) + "  " + name + "\n"
}

func TestCheckInstallsNewerRelease(t *testing.T) {
	newBin := []byte("#!/bin/sh\necho new\n")
	u := fakeGitHub(t, "v0.4.0", newBin, sumLine([]byte("other"), "gitreview-x-y")+sumLine(newBin, AssetName()))
	exe := filepath.Join(t.TempDir(), "gitreview")
	os.WriteFile(exe, []byte("old"), 0o755)

	if _, err := u.Check(context.Background(), "v0.4.0", exe); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("same version: %v, want ErrUpToDate", err)
	}
	tag, err := u.Check(context.Background(), "v0.3.0", exe)
	if err != nil || tag != "v0.4.0" {
		t.Fatalf("Check: %q, %v", tag, err)
	}
	got, _ := os.ReadFile(exe)
	st, _ := os.Stat(exe)
	if string(got) != string(newBin) || st.Mode().Perm() != 0o755 {
		t.Fatalf("binary not replaced: %q %v", got, st.Mode())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".gitreview-update-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestInstallRejectsBadChecksum(t *testing.T) {
	u := fakeGitHub(t, "v0.4.0", []byte("tampered"), sumLine([]byte("original"), AssetName()))
	exe := filepath.Join(t.TempDir(), "gitreview")
	os.WriteFile(exe, []byte("old"), 0o755)
	if _, err := u.Check(context.Background(), "v0.3.0", exe); err == nil {
		t.Fatal("expected checksum error")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatal("binary replaced despite bad checksum")
	}
}

func TestInstallRequiresListedAsset(t *testing.T) {
	u := fakeGitHub(t, "v0.4.0", []byte("x"), sumLine([]byte("x"), "gitreview-plan9-mips"))
	if err := u.Install(context.Background(), "v0.4.0", filepath.Join(t.TempDir(), "g")); err == nil {
		t.Fatal("expected error for an asset missing from SHA256SUMS")
	}
}
