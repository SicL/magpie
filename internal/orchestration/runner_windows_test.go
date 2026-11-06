package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows CLI distribution provides dsh.cmd. Verify that proc.StartTree can
// own and stop a batch shim and its child, not only a directly launched exe.
func TestRunnerWindowsCommandShim(t *testing.T) {
	for _, mode := range []string{"complete", "hang"} {
		t.Run(mode, func(t *testing.T) {
			store, runner, task := setupRunner(t, mode)
			path := filepath.Join(t.TempDir(), "dsh-fixture.cmd")
			binary := strings.ReplaceAll(os.Args[0], "\"", "\"\"")
			body := "@echo off\r\n\"" + binary + "\" -test.run=TestRunnerHelperProcess %*\r\n"
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			runner.command = func() (string, []string, error) { return path, nil, nil }
			run, err := runner.Start(task.ID, task.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "hang" {
				if err := runner.Stop(run.ID); err != nil {
					t.Fatal(err)
				}
			}
			runner.wait.Wait()
			result, err := store.Run(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if mode == "hang" {
				want = "cancelled"
			}
			if result.State != want {
				t.Fatalf("batch wrapper run: %+v", result)
			}
		})
	}
}
