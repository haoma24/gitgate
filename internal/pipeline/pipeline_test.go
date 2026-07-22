package pipeline_test

import (
	"context"
	"testing"

	"github.com/jvrsantacruz/gitgate/internal/pipeline"
)

// noopLogger is a Logger that discards all output.
type noopLogger struct{}

func (l *noopLogger) Log(step, level, message string) {}

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
