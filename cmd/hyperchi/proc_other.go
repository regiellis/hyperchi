//go:build !unix

package main

import (
	"errors"
	"os"
	"os/exec"
)

// setProcessGroup is a no-op where process groups are unavailable; the
// fallbacks below reach only the direct child.
func setProcessGroup(*exec.Cmd) {}

// interruptGroup kills the direct child: there is no portable SIGINT here
// (Windows rejects os.Interrupt), so waiting out the grace period would only
// delay the kill.
func interruptGroup(cmd *exec.Cmd) error {
	return killGroup(cmd)
}

// killGroup kills the direct child.
func killGroup(cmd *exec.Cmd) error {
	return ignoreFinished(cmd.Process.Kill())
}

func ignoreFinished(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
