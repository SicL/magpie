package orchestration

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// launchRun is called while RunService.mu is held. Both DSH and ZCode
// use the same durable lease and owned process-tree supervision. A plan
// run never records DSH-native session events even if stderr/stdout is forged.
func (r *RunService) launchRun(t Task, revision int64, cwd, adapter, executable string,
	args []string, prompt, sourceRunID string, safeEnv []string) (Run, error) {
	run, err := r.store.reserveRunAdapter(t, revision, cwd, adapter, sourceRunID)
	if err != nil {
		return Run{}, err
	}
	logPath, err := r.store.logFile(run.ID)
	if err != nil {
		_ = r.store.finishRun(run.ID, "failed", -1, "cannot prepare run log")
		return Run{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = r.store.finishRun(run.ID, "failed", -1, "cannot open run log")
		return Run{}, err
	}
	writer := &lineLog{file: logFile}
	if adapter == adapterDSH {
		writer.onSession = func(sid string) { _ = r.store.setRunSession(run.ID, sid) }
	}
	cmd := proc.Command(executable, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(prompt)
	if safeEnv != nil {
		cmd.Env = safeEnv
	} else if len(r.env) > 0 {
		cmd.Env = append(os.Environ(), r.env...)
	}
	cmd.Stdout = writer
	cmd.Stderr = agentStderr{log: writer}
	tree, err := proc.StartTree(cmd)
	if err != nil {
		_ = writer.Close()
		_ = r.store.finishRun(run.ID, "failed", -1, "Agent process could not start")
		return Run{}, fmt.Errorf("start %s: %w", adapter, err)
	}
	active := &liveRun{tree: tree}
	r.active[run.ID] = active
	r.wait.Add(1)
	if err = r.store.runStarted(run.ID, cmd.Process.Pid); err != nil {
		active.cancelling = true
		tree.Kill()
		go r.await(run.ID, active, writer)
		return Run{}, fmt.Errorf("persist %s process start: %w", adapter, err)
	}
	go r.await(run.ID, active, writer)
	result, err := r.store.Run(run.ID)
	if err != nil {
		return Run{}, fmt.Errorf("read started agent run %s: %w", run.ID, err)
	}
	return result, nil
}
