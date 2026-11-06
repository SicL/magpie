package orchestration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseGitHubCIRemoteStrict(t *testing.T) {
	valid := []string{
		"https://github.com/SicL/magpie.git",
		"https://github.com/SicL/magpie",
		"git@github.com:SicL/magpie.git",
		"ssh://git@github.com/SicL/magpie.git",
	}
	for _, v := range valid {
		got, ok := parseGitHubRemote(v)
		if !ok || got != "SicL/magpie" {
			t.Fatalf("rejected valid remote %s -> %s", v, got)
		}
	}
	invalid := []string{
		"http://github.com/SicL/magpie.git",
		"https://github.com.evil/SicL/magpie",
		"https://evil.com/SicL/magpie",
		"https://github.com/SicL/magpie?token=secret",
		"https://github.com/SicL/magpie#part",
		"https://github.com/SicL/../magpie",
		"git@github.com:../../something",
		"https://github.com/SicL",
		"https://github.com/SicL/magpie/extra",
		"file:///tmp/repo",
		"",
	}
	for _, v := range invalid {
		if got, ok := parseGitHubRemote(v); ok {
			t.Errorf("accepted unsafe remote %q -> %q", v, got)
		}
	}
}

func makeGitHubCITestServer(t *testing.T, sha, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected method %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/repos/example/project/commits/") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if mode == "redirect" {
			http.Redirect(w, r, "https://example.com/escaped", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/check-runs") {
			if mode == "huge" {
				fmt.Fprint(w, `{"total_count":101,"check_runs":[]}`)
				return
			}
			if mode == "missing" {
				fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
				return
			}
			head := sha
			if mode == "wrong_sha" {
				head = strings.Repeat("0", 40)
			}
			state, conclusion := "completed", "success"
			if mode == "pending" {
				state, conclusion = "in_progress", ""
			}
			if mode == "failed" {
				conclusion = "failure"
			}
			if conclusion == "" {
				fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"head_sha":%q,"status":%q,"conclusion":null}]}`, head, state)
			} else {
				fmt.Fprintf(w, `{"total_count":1,"check_runs":[{"head_sha":%q,"status":%q,"conclusion":%q}]}`, head, state, conclusion)
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/status") {
			if mode == "classic_failed" {
				fmt.Fprint(w, `{"state":"failure","statuses":[{"state":"failure"}]}`)
				return
			}
			fmt.Fprint(w, `{"state":"success","statuses":[]}`)
			return
		}
		t.Errorf("unexpected GitHub endpoint %s", r.URL.Path)
		http.NotFound(w, r)
	}))
}

func TestGitHubCIForCommitRequiresAllChecksGreen(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, c := range []struct{ mode, want string }{
		{"success", "verified"},
		{"missing", "no_checks"},
		{"pending", "pending"},
		{"failed", "failed"},
		{"classic_failed", "failed"},
		{"wrong_sha", "unavailable"},
		{"huge", "unavailable"},
		{"redirect", "unavailable"},
	} {
		t.Run(c.mode, func(t *testing.T) {
			server := makeGitHubCITestServer(t, sha, c.mode)
			defer server.Close()
			client := githubCIClient()
			status, _ := githubCIForCommit(context.Background(), client, server.URL, "example/project", sha)
			if status != c.want {
				t.Fatalf("mode=%s status=%s want=%s", c.mode, status, c.want)
			}
		})
	}
}

func TestGitHubCIHandoffRequiresHumanReceiptAndExactSHA(t *testing.T) {
	s, _, task := setupRunner(t, "complete")
	wt, err := s.worktreeForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	handoffGit(t, wt, "remote", "add", "origin", "https://github.com/example/project.git")
	preview, err := s.PreviewHandoff(task.ID)
	if err != nil || !preview.Ready {
		t.Fatalf("clean handoff expected %+v %v", preview, err)
	}
	server := makeGitHubCITestServer(t, preview.GitHead, "success")
	defer server.Close()
	client := githubCIClient()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	report, err := s.gitHubCIForHandoff(ctx, client, server.URL, task.ID)
	if err != nil || report.Status != "verified" || report.Checks != 1 || report.Repository != "example/project" {
		t.Fatalf("CI report %+v %v", report, err)
	}
	if _, err = s.verifyCIHandoff(ctx, client, server.URL, task.ID, task.Revision, preview.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("unacknowledged Git snapshot was attested: %v", err)
	}
	if _, err = s.AcknowledgeHandoff(task.ID, task.Revision, preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	proof, err := s.verifyCIHandoff(ctx, client, server.URL, task.ID, task.Revision, preview.Fingerprint)
	if err != nil || proof.GitHead != preview.GitHead || proof.Checks != 1 {
		t.Fatalf("CI proof %+v %v", proof, err)
	}
	if _, err = s.verifyCIHandoff(ctx, client, server.URL, task.ID, task.Revision, preview.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CI proof accepted: %v", err)
	}
	saved, err := s.HandoffCIProofs(task.ID)
	if err != nil || len(saved) != 1 || saved[0].Fingerprint != preview.Fingerprint {
		t.Fatalf("immutable CI history %+v %v", saved, err)
	}
	handoffGit(t, wt, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "new head")
	if _, err = s.verifyCIHandoff(ctx, client, server.URL, task.ID, task.Revision, preview.Fingerprint); !errors.Is(err, ErrConflict) {
		t.Fatalf("CI proof accepted for stale Worktree HEAD: %v", err)
	}
}

func TestGitHubCIHandoffUnsupportedRemoteCannotBecomeVerified(t *testing.T) {
	s, _, task := setupRunner(t, "complete")
	result, err := s.GitHubCIForHandoff(task.ID)
	if err != nil || result.Status != "unsupported_remote" {
		t.Fatalf("unregistered GitHub remote was trusted %+v %v", result, err)
	}
	if _, err = s.HandoffCIProofs("no-such-task"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown task proof lookup permitted %v", err)
	}
}
