//go:build !windows

package orchestration

import (
	"errors"
	"fmt"
	"syscall"
)

// A process group or child may still exist even after its parent vanished.
// Always require an explicit operator attestation and Git inspection.
func previousProcessGone(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("%w: old process PID is unknown", ErrConflict)
	}
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	if err == nil {
		return false, nil
	}
	return false, fmt.Errorf("%w: process liveness unknown: %v", ErrConflict, err)
}
