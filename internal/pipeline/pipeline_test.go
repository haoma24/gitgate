package pipeline_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jvrsantacruz/gitgate/internal/pipeline"
)

// noopLogger is a Logger that discards all output.
type noopLogger struct{}

func (l *noopLogger) Log(step, level, message string) {}

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// newWorktreeWithEmptyOrigin creates a git worktree with one commit whose
// 'origin' remote points at a freshly-initialised bare repo that has no
// branches yet (simulating the first push to an empty remote).
func newWorktreeWithEmptyOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")

	git(t, root, "init", "--bare", bare)
	git(t, root, "init", work)
	git(t, work, "config", "user.email", "test@example.com")
	git(t, work, "config", "user.name", "Test")
	git(t, work, "config", "commit.gpgsign", "false")
	if err := exec.Command("git", "-C", work, "commit", "--allow-empty", "-m", "init").Run(); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	git(t, work, "remote", "add", "origin", bare)
	return work
}

func TestPipeline_SkipSteps(t *testing.T) {
	pl := pipeline.New(pipeline.Config{
		WorktreePath: t.TempDir(),
		Branch:       "main",
		SkipSteps:    []string{"rebase", "review", "test", "lint", "push", "pr"},
		Logger:       &noopLogger{},
	})

	result, err := pl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Only 'intent' should run; rest should be skipped
	skipped := 0
	for _, sr := range result.StepResults {
		if sr.Status == "skip" {
			skipped++
		}
	}

	if skipped != 6 {
		t.Errorf("expected 6 skipped steps, got %d", skipped)
	}
}

func TestPipeline_RebaseSkipsWhenTargetMissingOnRemote(t *testing.T) {
	work := newWorktreeWithEmptyOrigin(t)

	pl := pipeline.New(pipeline.Config{
		WorktreePath: work,
		Branch:       "main",
		OriginRemote: "origin",
		// Run only intent + rebase; nothing downstream is relevant here.
		SkipSteps: []string{"review", "test", "lint", "push", "pr"},
		Logger:    &noopLogger{},
	})

	result, err := pl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected pipeline to succeed, got failure: %+v", result.StepResults)
	}

	var rebase *pipeline.StepResult
	for _, sr := range result.StepResults {
		if sr.Step == pipeline.StepRebase {
			rebase = sr
		}
	}
	if rebase == nil {
		t.Fatal("rebase step result not found")
	}
	if rebase.Status != "skip" {
		t.Errorf("expected rebase to skip when origin/main is missing, got %q (output: %s, err: %v)",
			rebase.Status, rebase.Output, rebase.Error)
	}
}

func TestPipeline_AllStepsOrdered(t *testing.T) {
	expectedOrder := []pipeline.StepName{
		pipeline.StepIntent,
		pipeline.StepRebase,
		pipeline.StepReview,
		pipeline.StepTest,
		pipeline.StepLint,
		pipeline.StepPush,
		pipeline.StepPR,
	}

	if len(pipeline.AllSteps) != len(expectedOrder) {
		t.Fatalf("expected %d steps, got %d", len(expectedOrder), len(pipeline.AllSteps))
	}

	for i, step := range pipeline.AllSteps {
		if step != expectedOrder[i] {
			t.Errorf("step %d: expected %s, got %s", i, expectedOrder[i], step)
		}
	}
}

func TestPipeline_ContextCancellation(t *testing.T) {
	pl := pipeline.New(pipeline.Config{
		WorktreePath: t.TempDir(),
		Branch:       "main",
		Logger:       &noopLogger{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := pl.Execute(ctx, nil)
	if err == nil {
		t.Error("expected error on cancelled context")
	}
}

func TestPipeline_OnStepCallback(t *testing.T) {
	called := make([]string, 0)

	pl := pipeline.New(pipeline.Config{
		WorktreePath: t.TempDir(),
		Branch:       "main",
		SkipSteps:    []string{"rebase", "review", "test", "lint", "push", "pr"},
		Logger:       &noopLogger{},
	})

	_, err := pl.Execute(context.Background(), func(step string) {
		called = append(called, step)
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(called) != 1 || called[0] != "intent" {
		t.Errorf("expected onStep called with 'intent', got %v", called)
	}
}
