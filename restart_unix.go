//go:build unix

package main

import (
	"os"
	"strings"
	"syscall"
)

// restart replaces the running process with exe, keeping arguments, PID and
// terminal. The access token is passed on so open pages stay logged in.
func restart(exe, token string) error {
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "GITREVIEW_TOKEN=") {
			env = append(env, e)
		}
	}
	if token != "" {
		env = append(env, "GITREVIEW_TOKEN="+token)
	}
	return syscall.Exec(exe, os.Args, env)
}
