//go:build !unix

package git

func agentSocket() string { return "" }
