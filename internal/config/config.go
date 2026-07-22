// Package config handles loading and validation of gitgate configuration.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure for gitgate.
type Config struct {
	// Version of the config format
	Version int `yaml:"version"`

	// Pipeline configuration
	Pipeline PipelineConfig `yaml:"pipeline"`

	// AI Review configuration
	Review ReviewConfig `yaml:"review"`

	// Test configuration
	Test TestConfig `yaml:"test"`

	// Lint configuration
	Lint LintConfig `yaml:"lint"`

	// Pull Request configuration
	PR PRConfig `yaml:"pr"`
}

// PipelineConfig controls which pipeline steps are enabled and their behavior.
type PipelineConfig struct {
	// Steps to skip (e.g., ["lint", "review"])
	Skip []string `yaml:"skip"`

	// MaxParallelRuns limits the number of concurrent pipeline runs.
	MaxParallelRuns int `yaml:"max_parallel_runs"`

	// TargetBranch is the branch to rebase onto (default: main or master)
	TargetBranch string `yaml:"target_branch"`
}

// ReviewConfig controls the AI review step.
type ReviewConfig struct {
	// Provider: "anthropic", "openai", "ollama", "cli"
	Provider string `yaml:"provider"`

	// Model name (e.g., "claude-opus-4-5", "gpt-4o")
	Model string `yaml:"model"`

	// CLICommand is used when provider is "cli" (e.g., "claude", "aider")
	CLICommand string `yaml:"cli_command"`

	// FailOn controls which severity levels fail the pipeline.
	// Values: "critical", "high", "medium", "low"
	FailOn string `yaml:"fail_on"`

	// MaxTokens limits the review response size.
	MaxTokens int `yaml:"max_tokens"`

	// SystemPrompt overrides the default review system prompt.
	SystemPrompt string `yaml:"system_prompt"`
}

// TestConfig controls the test step.
type TestConfig struct {
	// Command to run tests (e.g., "go test ./...", "npm test")
	Command string `yaml:"command"`

	// TimeoutSecs is the maximum time to wait for tests.
	TimeoutSecs int `yaml:"timeout_secs"`
}

// LintConfig controls the lint step.
type LintConfig struct {
	// Command to run linter (e.g., "golangci-lint run", "eslint .")
	Command string `yaml:"command"`

	// TimeoutSecs is the maximum time to wait for linting.
	TimeoutSecs int `yaml:"timeout_secs"`
}

// PRConfig controls pull request creation.
type PRConfig struct {
	// Draft creates PRs as drafts.
	Draft bool `yaml:"draft"`

	// Labels to add to created PRs.
	Labels []string `yaml:"labels"`

	// Template path for PR description.
	Template string `yaml:"template"`
}

// Load reads and parses a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return cfg, nil
}

// LoadOrDefault loads the config at path, returning DefaultConfig() if the file
// is missing or cannot be parsed. Use this on hot paths (the daemon) where a
// bad or absent config should degrade gracefully rather than abort the run.
func LoadOrDefault(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		return DefaultConfig()
	}
	return cfg
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Version: 1,
		Pipeline: PipelineConfig{
			MaxParallelRuns: 2,
			TargetBranch:    "main",
		},
		Review: ReviewConfig{
			Provider:  "anthropic",
			Model:     "claude-opus-4-5",
			FailOn:    "high",
			MaxTokens: 4096,
		},
		Test: TestConfig{
			TimeoutSecs: 300,
		},
		Lint: LintConfig{
			TimeoutSecs: 60,
		},
	}
}

// WriteSampleConfig writes a sample .gitgate.yml to the given path.
func WriteSampleConfig(path, originRemote string) error {
	sample := fmt.Sprintf(`# GitGate Configuration
# Documentation: https://github.com/jvrsantacruz/gitgate#configuration
version: 1

pipeline:
  # Steps to disable (remove from list to enable)
  # Available: intent, rebase, review, test, lint, push, pr
  skip: []

  # Maximum number of concurrent pipeline runs
  max_parallel_runs: 2

  # Branch to rebase onto before pushing
  target_branch: main

review:
  # AI provider for code review
  # Options: anthropic, openai, ollama, cli
  provider: anthropic

  # Model to use for review
  model: claude-opus-4-5

  # Minimum severity to fail the pipeline
  # Options: critical, high, medium, low
  fail_on: high

  # Optional: custom system prompt for the reviewer
  # system_prompt: |
  #   You are a strict code reviewer...

test:
  # Test command (auto-detected if not set)
  # command: go test ./...
  timeout_secs: 300

lint:
  # Lint command (auto-detected if not set)
  # command: golangci-lint run
  timeout_secs: 60

pr:
  # Create PRs as drafts
  draft: false

  # Labels to add to PRs
  labels: []

  # Path to PR template (relative to repo root)
  # template: .github/pull_request_template.md
`)

	return os.WriteFile(path, []byte(sample), 0o644)
}

// IsStepSkipped checks if a pipeline step should be skipped.
func (c *Config) IsStepSkipped(step string) bool {
	for _, s := range c.Pipeline.Skip {
		if s == step {
			return true
		}
	}
	return false
}
