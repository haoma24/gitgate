package daemon_test

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jvrsantacruz/gitgate/internal/daemon"
)

func setupTestDB(t *testing.T) *daemon.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := daemon.OpenDB(dir)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func makeTestRun(bareRepo, branch string) *daemon.Run {
	return &daemon.Run{
		ID:       uuid.New().String(),
		BareRepo: bareRepo,
		Ref:      "refs/heads/" + branch,
		Branch:   branch,
		OldSHA:   "0000000000000000000000000000000000000000",
		NewSHA:   "abc1234def5678901234567890123456789012345",
	}
}

func TestDB_CreateAndGetRun(t *testing.T) {
	db := setupTestDB(t)

	run := makeTestRun("/home/user/.gitgate/repos/abc.git", "feature/test")

	if err := db.CreateRun(run); err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	got, err := db.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}

	if got.ID != run.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, run.ID)
	}
	if got.Branch != run.Branch {
		t.Errorf("Branch mismatch: got %s, want %s", got.Branch, run.Branch)
	}
	if got.Status != daemon.RunStatusPending {
		t.Errorf("Status should be pending, got %s", got.Status)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

func TestDB_UpdateRunStatus(t *testing.T) {
	db := setupTestDB(t)
	run := makeTestRun("/bare.git", "main")
	if err := db.CreateRun(run); err != nil {
		t.Fatal(err)
	}

	transitions := []daemon.RunStatus{
		daemon.RunStatusRunning,
		daemon.RunStatusSuccess,
	}

	for _, status := range transitions {
		run.Status = status
		run.CurrentStep = "test"
		if err := db.UpdateRun(run); err != nil {
			t.Fatalf("UpdateRun to %s failed: %v", status, err)
		}

		got, err := db.GetRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != status {
			t.Errorf("expected status %s, got %s", status, got.Status)
		}
	}
}

func TestDB_ListRuns(t *testing.T) {
	db := setupTestDB(t)

	for i := range 5 {
		run := makeTestRun("/bare.git", "branch-"+string(rune('A'+i)))
		if err := db.CreateRun(run); err != nil {
			t.Fatalf("CreateRun %d failed: %v", i, err)
		}
		time.Sleep(time.Millisecond) // Ensure ordering
	}

	runs, err := db.ListRuns(3)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	if len(runs) != 3 {
		t.Errorf("expected 3 runs, got %d", len(runs))
	}
}

func TestDB_MarkCrashedRuns(t *testing.T) {
	db := setupTestDB(t)

	// Create one pending and one running run
	pending := makeTestRun("/bare.git", "pending-branch")
	running := makeTestRun("/bare.git", "running-branch")

	if err := db.CreateRun(pending); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(running); err != nil {
		t.Fatal(err)
	}

	running.Status = daemon.RunStatusRunning
	if err := db.UpdateRun(running); err != nil {
		t.Fatal(err)
	}

	// Create a completed run (should NOT be marked as crashed)
	done := makeTestRun("/bare.git", "done-branch")
	if err := db.CreateRun(done); err != nil {
		t.Fatal(err)
	}
	done.Status = daemon.RunStatusSuccess
	if err := db.UpdateRun(done); err != nil {
		t.Fatal(err)
	}

	// Mark crashes
	if err := db.MarkCrashedRuns(); err != nil {
		t.Fatalf("MarkCrashedRuns failed: %v", err)
	}

	// Verify pending and running are now crashed
	for _, id := range []string{pending.ID, running.ID} {
		got, err := db.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != daemon.RunStatusCrash {
			t.Errorf("run %s should be crashed, got %s", id, got.Status)
		}
	}

	// Verify done is still success
	gotDone, err := db.GetRun(done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotDone.Status != daemon.RunStatusSuccess {
		t.Errorf("completed run should still be success, got %s", gotDone.Status)
	}
}

func TestDB_Findings(t *testing.T) {
	db := setupTestDB(t)
	run := makeTestRun("/bare.git", "main")
	if err := db.CreateRun(run); err != nil {
		t.Fatal(err)
	}

	finding := &daemon.Finding{
		ID:          uuid.New().String(),
		RunID:       run.ID,
		Severity:    "high",
		File:        "main.go",
		Line:        42,
		Description: "potential nil pointer dereference",
		Action:      "ask-user",
	}

	if err := db.AddFinding(finding); err != nil {
		t.Fatalf("AddFinding failed: %v", err)
	}

	findings, err := db.GetFindings(run.ID)
	if err != nil {
		t.Fatalf("GetFindings failed: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.Severity != "high" {
		t.Errorf("severity mismatch")
	}
	if f.Status != "pending" {
		t.Errorf("expected pending status, got %s", f.Status)
	}

	// Update finding status
	if err := db.UpdateFindingStatus(finding.ID, "skipped"); err != nil {
		t.Fatalf("UpdateFindingStatus failed: %v", err)
	}

	findings2, _ := db.GetFindings(run.ID)
	if findings2[0].Status != "skipped" {
		t.Errorf("expected skipped, got %s", findings2[0].Status)
	}
}

func TestDB_Logs(t *testing.T) {
	db := setupTestDB(t)
	run := makeTestRun("/bare.git", "main")
	if err := db.CreateRun(run); err != nil {
		t.Fatal(err)
	}

	for i, msg := range []string{"starting", "working", "done"} {
		if err := db.AddLog(run.ID, "info", "test", msg); err != nil {
			t.Fatalf("AddLog %d failed: %v", i, err)
		}
	}

	logs, err := db.GetLogs(run.ID, 10)
	if err != nil {
		t.Fatalf("GetLogs failed: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("expected 3 log entries, got %d", len(logs))
	}
}

func TestDB_ListActiveRuns(t *testing.T) {
	db := setupTestDB(t)

	active := makeTestRun("/bare.git", "active")
	inactive := makeTestRun("/bare.git", "inactive")

	if err := db.CreateRun(active); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRun(inactive); err != nil {
		t.Fatal(err)
	}

	// Mark one as completed
	inactive.Status = daemon.RunStatusFailed
	if err := db.UpdateRun(inactive); err != nil {
		t.Fatal(err)
	}

	runs, err := db.ListActiveRuns()
	if err != nil {
		t.Fatalf("ListActiveRuns failed: %v", err)
	}

	if len(runs) != 1 {
		t.Errorf("expected 1 active run, got %d", len(runs))
	}
	if runs[0].ID != active.ID {
		t.Error("wrong run returned")
	}
}

// TestMain sets up any necessary test environment.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
