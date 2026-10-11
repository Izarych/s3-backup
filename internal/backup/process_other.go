//go:build !windows

package backup

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
