//go:build !unix

package main

import "errors"

func restart(string, string) error {
	return errors.New("automatic restart is not supported on this platform")
}
