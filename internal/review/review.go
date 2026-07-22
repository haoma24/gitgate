// Package review implements the AI code-review step of the GitGate pipeline.
// It defines a provider-agnostic interface plus concrete providers (Anthropic,
// OpenAI, Ollama, and an arbitrary local CLI agent).
package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNoAPIKey is returned by NewProvider when a cloud provider is selected but
// its API key environment variable is not set. Callers should treat this as a
// reason to skip (not fail) the review step.
var ErrNoAPIKey = errors.New("review: API key not configured")

// Finding is a single issue reported by the reviewer.
type Finding struct {
	Severity    string `json:"severity"` // critical, high, medium, low
	File        string `json:"file"`
	Line        int    `json:"line"`
	Description string `json:"description"`
	Action      string `json:"action"` // auto-fix, ask-user
}

// Request carries everything a provider needs to review a change.
type Request struct {
	Diff   string
	Intent string
}

// Options configures which provider is used and how it behaves. It maps
// directly from the `review:` block of .gitgate.yml.
type Options struct {
	Provider     string // anthropic | openai | ollama | cli
	Model        string
	CLICommand   string // used when Provider == "cli"
	MaxTokens    int
	SystemPrompt string
	FailOn       string // critical | high | medium | low
	BaseURL      string // optional endpoint override (mainly for testing/self-hosting)
}

// Provider is implemented by every supported review backend.
type Provider interface {
	// Name returns a short human-readable identifier (e.g. "anthropic").
	Name() string
	// Review analyses the diff and returns zero or more findings.
	Review(ctx context.Context, req Request) ([]Finding, error)
}

// NewProvider constructs the Provider selected by opts. For cloud providers it
// resolves the API key from the environment and returns ErrNoAPIKey if missing.
func NewProvider(opts Options) (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	if provider == "" {
		provider = "anthropic"
	}

	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 4096
	}
	if opts.SystemPrompt == "" {
		opts.SystemPrompt = defaultSystemPrompt
	}

	switch provider {
	case "anthropic":
		key := firstEnv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("%w (set ANTHROPIC_API_KEY)", ErrNoAPIKey)
		}
		model := opts.Model
		if model == "" {
			model = "claude-opus-4-5"
		}
		return &anthropicProvider{opts: opts, apiKey: key, model: model}, nil

	case "openai":
		key := firstEnv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("%w (set OPENAI_API_KEY)", ErrNoAPIKey)
		}
		model := opts.Model
		if model == "" {
			model = "gpt-4o"
		}
		return &openAIProvider{opts: opts, apiKey: key, model: model}, nil

	case "ollama":
		model := opts.Model
		if model == "" {
			model = "llama3.1"
		}
		return &ollamaProvider{opts: opts, model: model}, nil

	case "cli":
		if strings.TrimSpace(opts.CLICommand) == "" {
			return nil, errors.New("review: provider 'cli' requires review.cli_command in config")
		}
		return &cliProvider{opts: opts}, nil

	default:
		return nil, fmt.Errorf("review: unknown provider %q", provider)
	}
}

// severityRank maps a severity string to a comparable integer. Higher is more
// severe. Unknown severities rank as low.
func severityRank(sev string) int {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 1
	}
}

// MeetsThreshold reports whether a finding of severity sev is at or above the
// configured failOn level (and should therefore fail the pipeline).
func MeetsThreshold(sev, failOn string) bool {
	if strings.TrimSpace(failOn) == "" {
		failOn = "high"
	}
	return severityRank(sev) >= severityRank(failOn)
}
