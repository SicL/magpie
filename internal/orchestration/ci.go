package orchestration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

var githubCIName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

type GitHubCIStatus struct {
	TaskID      string `json:"taskId"`
	Fingerprint string `json:"fingerprint"`
	GitHead     string `json:"gitHead"`
	Repository  string `json:"repository,omitempty"`
	Status      string `json:"status"`
	Checks      int    `json:"checks"`
}

type HandoffCIProof struct {
	ID          string `json:"id"`
	TaskID      string `json:"taskId"`
	ReceiptID   string `json:"receiptId"`
	Fingerprint string `json:"fingerprint"`
	GitHead     string `json:"gitHead"`
	Repository  string `json:"repository"`
	Checks      int    `json:"checks"`
	CheckedAt   string `json:"checkedAt"`
}

func (s *Store) ensureCIProofSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_handoff_ci_proofs(
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,
  receipt_id TEXT NOT NULL UNIQUE REFERENCES agent_handoff_receipts(id) ON DELETE RESTRICT,
  fingerprint TEXT NOT NULL,
  git_head TEXT NOT NULL,
  repository TEXT NOT NULL,
  checks INTEGER NOT NULL CHECK(checks>0),
  checked_at TEXT NOT NULL
 )`)
	return err
}

func parseGitHubRemote(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	var repoPath string
	if strings.HasPrefix(raw, "git@github.com:") {
		repoPath = strings.TrimPrefix(raw, "git@github.com:")
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() != "github.com" || u.RawQuery != "" || u.Fragment != "" || u.User == nil && u.Scheme == "ssh" {
			return "", false
		}
		if u.Scheme != "https" && u.Scheme != "ssh" {
			return "", false
		}
		if u.Port() != "" || u.Opaque != "" || u.RawPath != "" {
			return "", false
		}
		if u.User != nil && u.User.Username() != "git" {
			return "", false
		}
		repoPath = strings.TrimPrefix(u.Path, "/")
	}
	repoPath = strings.TrimSuffix(repoPath, ".git")
	parts := strings.Split(repoPath, "/")
	if len(parts) != 2 || !githubCIName.MatchString(parts[0]) || !githubCIName.MatchString(parts[1]) ||
		parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func githubCIClient() *http.Client {
	return &http.Client{
		Timeout:       7 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 5 * time.Second,
		},
	}
}

func ciReadJSON(ctx context.Context, client *http.Client, endpoint string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Magpie-orchestration-ci/1")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	const max = 512 * 1024
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil || len(b) > max {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if dec.Decode(out) != nil {
		return false
	}
	var extra any
	return dec.Decode(&extra) == io.EOF
}

func githubCIForCommit(ctx context.Context, client *http.Client, base, repo, sha string) (string, int) {
	if len(sha) != 40 {
		return "unavailable", 0
	}
	for _, c := range sha {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "unavailable", 0
		}
	}
	urlBase := strings.TrimRight(base, "/")
	urlPath := "/repos/" + repo + "/commits/" + sha
	var checks struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			HeadSHA    string  `json:"head_sha"`
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if !ciReadJSON(ctx, client, urlBase+urlPath+"/check-runs?per_page=100", &checks) {
		return "unavailable", 0
	}
	if checks.TotalCount < 0 || checks.TotalCount > 100 || checks.TotalCount != len(checks.CheckRuns) {
		return "unavailable", 0
	}
	var combined struct {
		State    string `json:"state"`
		Statuses []struct {
			State string `json:"state"`
		} `json:"statuses"`
	}
	if !ciReadJSON(ctx, client, urlBase+urlPath+"/status", &combined) {
		return "unavailable", 0
	}
	if len(combined.Statuses) > 100 {
		return "unavailable", 0
	}
	count := checks.TotalCount + len(combined.Statuses)
	if count == 0 {
		return "no_checks", 0
	}
	pending, failed := false, false
	for _, run := range checks.CheckRuns {
		if run.HeadSHA != sha {
			return "unavailable", count
		}
		if run.Status != "completed" || run.Conclusion == nil {
			pending = true
			continue
		}
		if *run.Conclusion != "success" {
			failed = true
		}
	}
	for _, status := range combined.Statuses {
		switch status.State {
		case "success":
		case "pending":
			pending = true
		case "failure", "error":
			failed = true
		default:
			return "unavailable", count
		}
	}
	if failed {
		return "failed", count
	}
	if pending {
		return "pending", count
	}
	if len(combined.Statuses) > 0 && combined.State != "success" {
		return "unavailable", count
	}
	return "verified", count
}

// Production calls only the fixed GitHub API host; the base parameter exists
// in a private helper for local httptest fixtures, never from user input.
func (s *Store) gitHubCIForHandoff(ctx context.Context, client *http.Client, apiBase, taskID string) (GitHubCIStatus, error) {
	p, err := s.PreviewHandoff(taskID)
	if err != nil {
		return GitHubCIStatus{}, err
	}
	result := GitHubCIStatus{TaskID: taskID, Fingerprint: p.Fingerprint, GitHead: p.GitHead, Status: "handoff_blocked"}
	if !p.Ready {
		return result, nil
	}
	out, err := proc.CommandContext(ctx, "git", "-C", p.WorktreePath, "remote", "get-url", "origin").Output()
	if err != nil {
		result.Status = "unsupported_remote"
		return result, nil
	}
	remote, ok := parseGitHubRemote(string(out))
	if !ok {
		result.Status = "unsupported_remote"
		return result, nil
	}
	result.Repository = remote
	result.Status, result.Checks = githubCIForCommit(ctx, client, apiBase, remote, p.GitHead)
	return result, nil
}

func (s *Store) GitHubCIForHandoff(taskID string) (GitHubCIStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	return s.gitHubCIForHandoff(ctx, githubCIClient(), "https://api.github.com", taskID)
}

// A verified CI proof requires a separate, existing manual receipt and
// a LIVE GitHub check of the SAME immutable SHA and handoff fingerprint.
func (s *Store) verifyCIHandoff(ctx context.Context, client *http.Client, base, taskID string, rev int64, fingerprint string) (HandoffCIProof, error) {
	if rev <= 0 || len(fingerprint) != 64 {
		return HandoffCIProof{}, ErrInvalid
	}
	report, err := s.gitHubCIForHandoff(ctx, client, base, taskID)
	if err != nil {
		return HandoffCIProof{}, err
	}
	if report.Status != "verified" || report.Fingerprint != fingerprint || report.Checks < 1 {
		return HandoffCIProof{}, fmt.Errorf("%w: GitHub CI checks are not verified for the current handoff snapshot", ErrConflict)
	}
	current, err := s.PreviewHandoff(taskID)
	if err != nil {
		return HandoffCIProof{}, err
	}
	if !current.Ready || current.Revision != rev || current.Fingerprint != fingerprint || current.GitHead != report.GitHead {
		return HandoffCIProof{}, fmt.Errorf("%w: Git handoff changed during GitHub CI check", ErrConflict)
	}
	var receiptID string
	err = s.db.QueryRow("SELECT id FROM agent_handoff_receipts WHERE task_id=? AND revision=? AND fingerprint=?", taskID, rev, fingerprint).Scan(&receiptID)
	if errors.Is(err, sql.ErrNoRows) {
		return HandoffCIProof{}, fmt.Errorf("%w: manual handoff must be acknowledged before attaching CI evidence", ErrConflict)
	}
	if err != nil {
		return HandoffCIProof{}, err
	}
	id, err := newID()
	if err != nil {
		return HandoffCIProof{}, err
	}
	proof := HandoffCIProof{ID: id, TaskID: taskID, ReceiptID: receiptID, Fingerprint: fingerprint, GitHead: report.GitHead, Repository: report.Repository, Checks: report.Checks, CheckedAt: now()}
	res, err := s.db.Exec(`INSERT INTO agent_handoff_ci_proofs(id,task_id,receipt_id,fingerprint,git_head,repository,checks,checked_at)
  SELECT ?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM tasks WHERE id=? AND revision=? AND status='ready')`,
		proof.ID, proof.TaskID, proof.ReceiptID, proof.Fingerprint, proof.GitHead, proof.Repository, proof.Checks, proof.CheckedAt, taskID, rev)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return HandoffCIProof{}, ErrConflict
		}
		return HandoffCIProof{}, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return HandoffCIProof{}, err
	}
	if changed != 1 {
		return HandoffCIProof{}, ErrConflict
	}
	return proof, nil
}

func (s *Store) VerifyCIHandoff(taskID string, rev int64, fingerprint string) (HandoffCIProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Second)
	defer cancel()
	return s.verifyCIHandoff(ctx, githubCIClient(), "https://api.github.com", taskID, rev, fingerprint)
}

func (s *Store) HandoffCIProofs(taskID string) ([]HandoffCIProof, error) {
	if _, err := s.taskByID(taskID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query("SELECT id,task_id,receipt_id,fingerprint,git_head,repository,checks,checked_at FROM agent_handoff_ci_proofs WHERE task_id=? ORDER BY checked_at DESC,id DESC", taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HandoffCIProof{}
	for rows.Next() {
		var p HandoffCIProof
		if err := rows.Scan(&p.ID, &p.TaskID, &p.ReceiptID, &p.Fingerprint, &p.GitHead, &p.Repository, &p.Checks, &p.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
