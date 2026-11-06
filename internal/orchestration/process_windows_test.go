//go:build windows

package orchestration

import (
	"os"
	"testing"
)

func TestPreviousProcessGoneWindowsConservative(t *testing.T) {
	gone, err := previousProcessGone(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if gone {
		t.Fatal("live Magpie test process was treated as stopped")
	}
	gone, err = previousProcessGone(2147483000)
	if err != nil {
		t.Fatal(err)
	}
	if !gone {
		t.Fatal("known nonexistent process must report absent")
	}
	if gone, err = previousProcessGone(0); err == nil || gone {
		t.Fatal("unknown zero PID must stay blocked")
	}
}
