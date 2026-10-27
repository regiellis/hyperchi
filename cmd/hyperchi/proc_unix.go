//go:build unix

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

// setProcessGroup makes cmd the leader of a new process group (pgid == pid),
// which the children it spawns inherit.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interruptGroup sends SIGINT to every process in cmd's group. A group that
// has already exited is not an error.
func interruptGroup(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGINT)
}

// killGroup sends SIGKILL to every process left in cmd's group.
func killGroup(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	// The negative pid addresses the whole group led by cmd.
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
