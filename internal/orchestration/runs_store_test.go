package orchestration

import (
	"database/sql"
	_ "github.com/yetone/magpie/internal/sqlitedb"
	"path/filepath"
	"testing"
)

// A local development database made before DSH session capture is migrated
// in place. The migration must not overwrite the legacy Run table.
func TestRunSessionColumnMigratesInPlace(t *testing.T) {
	file := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE agent_runs (id TEXT PRIMARY KEY, task_id TEXT, adapter TEXT, state TEXT, pid INTEGER, worktree_path TEXT, started_at TEXT, finished_at TEXT, exit_code INTEGER, failure TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.db.Query("PRAGMA table_info(agent_runs)")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	resumed := false
	for rows.Next() {
		var position, required, pk int
		var name, kind string
		var def sql.NullString
		if err := rows.Scan(&position, &name, &kind, &required, &def, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "session_id" {
			found = true
		}
		if name == "resume_of" {
			resumed = true
		}
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !found || !resumed {
		t.Fatal("existing run table did not gain session and resume columns")
	}
	if err = store.ensureRunSchema(); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
