package orchestration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSessionOnlyFromDSHStdout(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "run.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	seen := ""
	log := &lineLog{file: f, onSession: func(id string) { seen = id }}
	bad := agentStderr{log: log}
	if _, err = bad.Write([]byte("{\"type\":\"session\",\"sessionId\":\"untrusted-stderr\"}\n")); err != nil {
		t.Fatal(err)
	}
	if seen != "" {
		t.Fatalf("stderr forged native session: %q", seen)
	}
	if _, err = log.Write([]byte("{\"type\":\"session\",\"sessionId\":\"session-trusted\"}\n")); err != nil {
		t.Fatal(err)
	}
	if seen != "session-trusted" {
		t.Fatalf("valid stdout session missing: %q", seen)
	}
	if err = log.Close(); err != nil {
		t.Fatal(err)
	}
}
