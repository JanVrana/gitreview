//go:build unix

package git

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAgent answers identity requests with n keys.
func fakeAgent(t *testing.T, n byte) string {
	p := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 5)
			c.Read(buf)
			c.Write([]byte{0, 0, 0, 5, 12, 0, 0, 0, n})
			c.Close()
		}
	}()
	return p
}

func TestAgentSocket(t *testing.T) {
	empty, full := fakeAgent(t, 0), fakeAgent(t, 2)
	if n, err := agentKeys(full); err != nil || n != 2 {
		t.Fatalf("agentKeys: %d %v", n, err)
	}
	t.Setenv("SSH_AUTH_SOCK", full)
	if s := agentSocket(); s != full {
		t.Errorf("inherited agent with keys: got %q", s)
	}
	// an empty or dead inherited agent is replaced by one with keys
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(filepath.Dir(full))) // no candidates there
	for _, env := range []string{empty, "/nonexistent/agent"} {
		t.Setenv("SSH_AUTH_SOCK", env)
		if s := agentSocket(); s == env {
			t.Errorf("%s kept", env)
		}
	}
}

func TestErrorShowsCommand(t *testing.T) {
	r := &Repo{Root: t.TempDir() + "/it's"}
	_, err := r.run("status")
	if err == nil || !strings.Contains(err.Error(), "\n$ git -C '"+r.Root[:len(r.Root)-4]+"it'\\''s' --no-optional-locks") {
		t.Fatalf("%v", err)
	}
	if q := shellQuote("a b"); q != "'a b'" {
		t.Errorf("quote: %s", q)
	}
}
