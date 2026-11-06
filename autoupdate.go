package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/JanVrana/gitreview/internal/server"
	"github.com/JanVrana/gitreview/internal/update"
)

const (
	firstUpdateCheck = 30 * time.Second
	updateInterval   = 6 * time.Hour
)

// autoUpdate periodically installs newer releases over the running binary
// and restarts into them. Open pages reconnect and reload by themselves.
func autoUpdate(srv *server.Server, token string) {
	exe, err := update.Executable()
	if err != nil {
		log.Printf("auto-update disabled: %v", err)
		return
	}
	u := update.New()
	lastErr := ""
	time.Sleep(firstUpdateCheck)
	for ; ; time.Sleep(updateInterval) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		tag, err := u.Check(ctx, buildVersion(), exe)
		cancel()
		switch {
		case errors.Is(err, update.ErrUpToDate):
			lastErr = ""
			continue
		case err != nil:
			if err.Error() != lastErr { // don't repeat the same failure every interval
				log.Printf("auto-update: %v", err)
				lastErr = err.Error()
			}
			continue
		}
		log.Printf("auto-update: installed %s (was %s), restarting", tag, buildVersion())
		srv.PrepareRestart()
		err = restart(exe, token)
		srv.AbortRestart()
		log.Printf("auto-update: restart failed, restart gitreview manually to use %s: %v", tag, err)
		return
	}
}

// uiUpdater lets the UI check for and install updates on demand (also with
// -no-auto-update). Development builds count as v0.0.0, like -update.
type uiUpdater struct {
	u     *update.Updater
	exe   string
	token string
	auto  bool
}

func newUIUpdater(token string, auto bool) server.Updater {
	exe, err := update.Executable()
	if err != nil {
		return nil
	}
	return &uiUpdater{u: update.New(), exe: exe, token: token, auto: auto}
}

func comparableVersion() string {
	if v := buildVersion(); update.Release(v) {
		return v
	}
	return "v0.0.0"
}

func (x *uiUpdater) Latest(ctx context.Context) (string, bool, error) {
	tag, err := x.u.Latest(ctx)
	if err != nil {
		return "", false, err
	}
	return tag, update.Newer(tag, comparableVersion()), nil
}

func (x *uiUpdater) Install(ctx context.Context) (string, error) {
	tag, err := x.u.Check(ctx, comparableVersion(), x.exe)
	if err == nil {
		log.Printf("update: installed %s (was %s) on request, restarting", tag, buildVersion())
	}
	return tag, err
}

func (x *uiUpdater) Restart() error { return restart(x.exe, x.token) }
func (x *uiUpdater) Auto() bool     { return x.auto }

// updateNow implements -update: install the latest release and exit.
func updateNow() error {
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tag, err := update.New().Check(ctx, comparableVersion(), exe)
	if errors.Is(err, update.ErrUpToDate) {
		fmt.Printf("gitreview %s is up to date\n", buildVersion())
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("gitreview updated %s → %s (%s)\n", buildVersion(), tag, exe)
	return nil
}
