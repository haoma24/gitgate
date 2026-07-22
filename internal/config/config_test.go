package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jvrsantacruz/gitgate/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if cfg.Pipeline.MaxParallelRuns != 2 {
		t.Errorf("expected max_parallel_runs 2, got %d", cfg.Pipeline.MaxParallelRuns)
	}
	if cfg.Review.Provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %s", cfg.Review.Provider)
	}
	if cfg.Review.FailOn != "high" {
		t.Errorf("expected fail_on high, got %s", cfg.Review.FailOn)
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".gitgate.yml")

	content := `
version: 1
pipeline:
  skip: [lint]
  max_parallel_runs: 4
  target_branch: develop
review:
  provider: openai
  model: gpt-4o
  fail_on: critical
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Pipeline.MaxParallelRuns != 4 {
		t.Errorf("expected 4, got %d", cfg.Pipeline.MaxParallelRuns)
	}
	if cfg.Pipeline.TargetBranch != "develop" {
		t.Errorf("expected develop, got %s", cfg.Pipeline.TargetBranch)
	}
	if cfg.Review.Provider != "openai" {
		t.Errorf("expected openai, got %s", cfg.Review.Provider)
	}
	if len(cfg.Pipeline.Skip) != 1 || cfg.Pipeline.Skip[0] != "lint" {
		t.Errorf("expected skip=[lint], got %v", cfg.Pipeline.Skip)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := config.Load("/nonexistent/.gitgate.yml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestIsStepSkipped(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Pipeline.Skip = []string{"lint", "review"}

	if !cfg.IsStepSkipped("lint") {
		t.Error("expected lint to be skipped")
	}
	if !cfg.IsStepSkipped("review") {
		t.Error("expected review to be skipped")
	}
	if cfg.IsStepSkipped("test") {
		t.Error("expected test NOT to be skipped")
	}
}

func TestWriteSampleConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".gitgate.yml")

	if err := config.WriteSampleConfig(cfgPath, "origin"); err != nil {
		t.Fatalf("WriteSampleConfig failed: %v", err)
	}

	// Verify file exists and is parseable
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("sample config is not parseable: %v", err)
	}

	if cfg.Version != 1 {
		t.Errorf("sample config version should be 1, got %d", cfg.Version)
	}
}
