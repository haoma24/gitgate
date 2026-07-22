// Package pipeline implements the GitGate quality gate pipeline.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jvrsantacruz/gitgate/internal/review"
)

// StepName identifies a pipeline step.
type StepName string

const (
	StepIntent StepName = "intent"
	StepRebase StepName = "rebase"
	StepReview StepName = "review"
	StepTest   StepName = "test"
	StepLint   StepName = "lint"
	StepPush   StepName = "push"
	StepPR     StepName = "pr"
)

// AllSteps is the ordered list of all pipeline steps.
var AllSteps = []StepName{
	StepIntent,
	StepRebase,
	StepReview,
	StepTest,
	StepLint,
	StepPush,
	StepPR,
}

// StepResult holds the outcome of a single pipeline step.
type StepResult struct {
	Step     StepName
	Status   string // "pass", "fail", "skip", "waiting"
	Output   string
	Findings []*Finding
	Error    error
	Duration time.Duration
}

// Finding is a single item found during the review step.
type Finding struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"` // critical, high, medium, low
	File        string `json:"file"`
	Line        int    `json:"line"`
	Description string `json:"description"`
	Action      string `json:"action"` // auto-fix, ask-user
}

// Result is the final result of a pipeline execution.
type Result struct {
	Success        bool
	NeedsUserInput bool
	WaitingStep    string
	StepResults    []*StepResult
}

// Logger is the interface for pipeline logging.
type Logger interface {
	Log(step, level, message string)
}

// Config holds all inputs needed to execute the pipeline.
type Config struct {
	WorktreePath string
	BareRepo     string
	Branch       string
	NewSHA       string
	Intent       string
	OriginURL    string
	OriginRemote string
	SkipSteps    []string

	// TargetBranch is the branch to rebase onto and diff against (default "main").
	TargetBranch string

	// Test/Lint overrides. When a command is empty it is auto-detected from
	// marker files in the worktree. Timeouts of 0 mean "no timeout".
	TestCommand string
	TestTimeout time.Duration
	LintCommand string
	LintTimeout time.Duration

	// Review configures the AI review step.
	Review review.Options

	Logger Logger
}

// targetBranch returns the configured target branch or the "main" default.
func (c Config) targetBranch() string {
	if strings.TrimSpace(c.TargetBranch) != "" {
		return c.TargetBranch
	}
	return "main"
}

// Pipeline orchestrates the sequential execution of pipeline steps.
type Pipeline struct {
	cfg   Config
	steps map[StepName]StepRunner
}

// StepRunner is a function that executes a single pipeline step.
type StepRunner func(ctx context.Context, cfg Config) *StepResult

// New creates a new Pipeline with the given configuration.
func New(cfg Config) *Pipeline {
	p := &Pipeline{cfg: cfg}
	p.steps = map[StepName]StepRunner{
		StepIntent: p.runIntent,
		StepRebase: p.runRebase,
		StepReview: p.runReview,
		StepTest:   p.runTest,
		StepLint:   p.runLint,
		StepPush:   p.runPush,
		StepPR:     p.runPR,
	}
	return p
}

// Execute runs all pipeline steps in order, calling onStep before each step.
func (p *Pipeline) Execute(ctx context.Context, onStep func(step string)) (*Result, error) {
	result := &Result{}
	skipSet := toSet(p.cfg.SkipSteps)

	for _, stepName := range AllSteps {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		// Check if step should be skipped
		if _, shouldSkip := skipSet[string(stepName)]; shouldSkip {
			result.StepResults = append(result.StepResults, &StepResult{
				Step:   stepName,
				Status: "skip",
			})
			p.log(string(stepName), "info", "step skipped by configuration")
			continue
		}

		if onStep != nil {
			onStep(string(stepName))
		}

		runner, ok := p.steps[stepName]
		if !ok {
			return result, fmt.Errorf("unknown step: %s", stepName)
		}

		p.log(string(stepName), "info", fmt.Sprintf("starting step %s", stepName))
		start := time.Now()
		sr := runner(ctx, p.cfg)
		sr.Duration = time.Since(start)
		result.StepResults = append(result.StepResults, sr)

		p.log(string(stepName), "info", fmt.Sprintf("step %s completed: %s (%.2fs)", stepName, sr.Status, sr.Duration.Seconds()))

		switch sr.Status {
		case "fail":
			if sr.Error != nil {
				p.log(string(stepName), "error", fmt.Sprintf("step %s failed: %v", stepName, sr.Error))
			}
			if out := strings.TrimSpace(sr.Output); out != "" {
				p.log(string(stepName), "error", fmt.Sprintf("step %s output:\n%s", stepName, out))
			}
			result.Success = false
			return result, nil
		case "waiting":
			result.NeedsUserInput = true
			result.WaitingStep = string(stepName)
			return result, nil
		}
	}

	result.Success = true
	return result, nil
}

// ── Step implementations (stubs / basic versions) ─────────────────────────────

func (p *Pipeline) runIntent(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepIntent}

	intent := cfg.Intent
	if intent == "" {
		// Try to infer from commit message
		out, err := runGitInWorktree(cfg.WorktreePath, "log", "--format=%s", "-1")
		if err == nil {
			intent = strings.TrimSpace(out)
		}
	}

	if intent == "" {
		intent = fmt.Sprintf("push %s to %s", cfg.Branch, cfg.OriginRemote)
	}

	sr.Output = fmt.Sprintf("Intent: %s", intent)
	sr.Status = "pass"
	return sr
}

func (p *Pipeline) runRebase(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepRebase}

	// Fetch latest from origin
	p.log("rebase", "info", "fetching latest from origin")
	if out, err := runCmdInDir(ctx, cfg.WorktreePath, "git", "fetch", cfg.OriginRemote); err != nil {
		sr.Status = "fail"
		sr.Error = fmt.Errorf("fetch failed: %w", err)
		sr.Output = out
		return sr
	}

	// Attempt rebase
	targetBranch := fmt.Sprintf("%s/%s", cfg.OriginRemote, cfg.targetBranch())
	p.log("rebase", "info", fmt.Sprintf("rebasing onto %s", targetBranch))
	if out, err := runCmdInDir(ctx, cfg.WorktreePath, "git", "rebase", targetBranch); err != nil {
		sr.Status = "fail"
		sr.Error = fmt.Errorf("rebase failed: %w\nOutput: %s", err, out)
		sr.Output = out
		return sr
	}

	sr.Status = "pass"
	sr.Output = fmt.Sprintf("Successfully rebased onto %s", targetBranch)
	return sr
}

func (p *Pipeline) runReview(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepReview}

	provider, err := review.NewProvider(cfg.Review)
	if err != nil {
		// Missing API key or unavailable provider is a skip, not a failure:
		// we never want a misconfigured reviewer to block an otherwise-good push.
		sr.Status = "skip"
		if errors.Is(err, review.ErrNoAPIKey) {
			sr.Output = fmt.Sprintf("review skipped: %v", err)
		} else {
			sr.Output = fmt.Sprintf("review unavailable: %v", err)
		}
		p.log("review", "info", sr.Output)
		return sr
	}

	// Compute the diff introduced by this branch relative to the target.
	targetRef := fmt.Sprintf("%s/%s", cfg.OriginRemote, cfg.targetBranch())
	diff, derr := runGitInWorktree(cfg.WorktreePath, "diff", targetRef+"..HEAD")
	if derr != nil || strings.TrimSpace(diff) == "" {
		// Fall back to the tip commit if the target ref is unavailable
		// (e.g. rebase was skipped or origin has no such branch yet).
		diff, _ = runGitInWorktree(cfg.WorktreePath, "show", "--format=medium", "HEAD")
	}
	if strings.TrimSpace(diff) == "" {
		sr.Status = "pass"
		sr.Output = "review: empty diff, nothing to review"
		return sr
	}

	p.log("review", "info", fmt.Sprintf("running AI review via %s", provider.Name()))
	findings, rerr := provider.Review(ctx, review.Request{Diff: diff, Intent: cfg.Intent})
	if rerr != nil {
		// Provider/transport errors skip rather than fail, for the same reason
		// as above. The error is recorded so the user can investigate.
		sr.Status = "skip"
		sr.Output = fmt.Sprintf("review error (skipped): %v", rerr)
		sr.Error = rerr
		p.log("review", "warn", sr.Output)
		return sr
	}

	failOn := cfg.Review.FailOn
	if failOn == "" {
		failOn = "high"
	}

	var failing int
	for _, f := range findings {
		sr.Findings = append(sr.Findings, &Finding{
			Severity:    f.Severity,
			File:        f.File,
			Line:        f.Line,
			Description: f.Description,
			Action:      f.Action,
		})
		if review.MeetsThreshold(f.Severity, failOn) {
			failing++
		}
	}

	if failing > 0 {
		sr.Status = "fail"
		sr.Error = fmt.Errorf("%d finding(s) at or above severity %q", failing, failOn)
		sr.Output = fmt.Sprintf("AI review: %d finding(s), %d blocking (fail_on=%s)", len(findings), failing, failOn)
	} else {
		sr.Status = "pass"
		sr.Output = fmt.Sprintf("AI review: %d finding(s), none blocking (fail_on=%s)", len(findings), failOn)
	}
	return sr
}

func (p *Pipeline) runTest(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepTest}

	// Use the configured command if set, otherwise auto-detect.
	cmd, args := parseCommand(cfg.TestCommand)
	if cmd == "" {
		cmd, args = detectTestCommand(cfg.WorktreePath)
	}
	if cmd == "" {
		sr.Status = "skip"
		sr.Output = "No test command detected"
		return sr
	}

	stepCtx, cancel := withTimeout(ctx, cfg.TestTimeout)
	defer cancel()

	p.log("test", "info", fmt.Sprintf("running: %s %s", cmd, strings.Join(args, " ")))
	out, err := runCmdInDir(stepCtx, cfg.WorktreePath, cmd, args...)
	sr.Output = out

	if err != nil {
		sr.Status = "fail"
		sr.Error = fmt.Errorf("tests failed: %w", timeoutErr(stepCtx, err))
		return sr
	}

	sr.Status = "pass"
	return sr
}

func (p *Pipeline) runLint(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepLint}

	cmd, args := parseCommand(cfg.LintCommand)
	if cmd == "" {
		cmd, args = detectLintCommand(cfg.WorktreePath)
	}
	if cmd == "" {
		sr.Status = "skip"
		sr.Output = "No lint command detected"
		return sr
	}

	stepCtx, cancel := withTimeout(ctx, cfg.LintTimeout)
	defer cancel()

	p.log("lint", "info", fmt.Sprintf("running: %s %s", cmd, strings.Join(args, " ")))
	out, err := runCmdInDir(stepCtx, cfg.WorktreePath, cmd, args...)
	sr.Output = out

	if err != nil {
		sr.Status = "fail"
		sr.Error = fmt.Errorf("lint failed: %w", timeoutErr(stepCtx, err))
		return sr
	}

	sr.Status = "pass"
	return sr
}

func (p *Pipeline) runPush(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepPush}

	p.log("push", "info", fmt.Sprintf("pushing %s to %s", cfg.Branch, cfg.OriginRemote))

	// Safety check: verify we won't overwrite remote commits
	remoteRef := fmt.Sprintf("%s/%s", cfg.OriginRemote, cfg.Branch)
	remoteHead, err := runGitInWorktree(cfg.WorktreePath, "rev-parse", "--verify", remoteRef)
	if err == nil {
		remoteHead = strings.TrimSpace(remoteHead)
		// Check if remote head is an ancestor of our new SHA
		_, err := runGitInWorktree(cfg.WorktreePath, "merge-base", "--is-ancestor", remoteHead, cfg.NewSHA)
		if err != nil {
			sr.Status = "fail"
			sr.Error = fmt.Errorf("remote has commits not in your branch; force push would lose data")
			return sr
		}
	}

	// Do the push. The worktree is detached, so <src> is a bare commit object and
	// git cannot infer the destination refname for a branch that does not exist on
	// the remote yet; the destination must be fully qualified.
	out, err := runCmdInDir(ctx, cfg.WorktreePath, "git", "push", cfg.OriginRemote,
		fmt.Sprintf("HEAD:refs/heads/%s", cfg.Branch))
	sr.Output = out

	if err != nil {
		sr.Status = "fail"
		sr.Error = fmt.Errorf("push failed: %w", err)
		return sr
	}

	sr.Status = "pass"
	return sr
}

func (p *Pipeline) runPR(ctx context.Context, cfg Config) *StepResult {
	sr := &StepResult{Step: StepPR}

	// Try gh first (GitHub), then glab (GitLab)
	if isCommandAvailable("gh") {
		out, err := runCmdInDir(ctx, cfg.WorktreePath, "gh", "pr", "create",
			"--fill", "--head", cfg.Branch)
		sr.Output = out
		if err != nil {
			// PR might already exist; try to update
			if strings.Contains(out, "already exists") {
				sr.Status = "pass"
				sr.Output = "PR already exists"
				return sr
			}
			sr.Status = "fail"
			sr.Error = fmt.Errorf("gh pr create failed: %w", err)
			return sr
		}
		sr.Status = "pass"
		return sr
	}

	if isCommandAvailable("glab") {
		out, err := runCmdInDir(ctx, cfg.WorktreePath, "glab", "mr", "create",
			"--fill", "--source-branch", cfg.Branch)
		sr.Output = out
		if err != nil {
			sr.Status = "fail"
			sr.Error = fmt.Errorf("glab mr create failed: %w", err)
			return sr
		}
		sr.Status = "pass"
		return sr
	}

	sr.Status = "skip"
	sr.Output = "No PR tool found (install gh or glab to enable PR creation)"
	return sr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (p *Pipeline) log(step, level, message string) {
	if p.cfg.Logger != nil {
		p.cfg.Logger.Log(step, level, message)
	}
}

func runGitInWorktree(worktreePath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runCmdInDir(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func detectTestCommand(worktreePath string) (string, []string) {
	checks := []struct {
		file string
		cmd  string
		args []string
	}{
		{"go.mod", "go", []string{"test", "./..."}},
		{"package.json", "npm", []string{"test"}},
		{"Cargo.toml", "cargo", []string{"test"}},
		{"pyproject.toml", "pytest", nil},
		{"setup.py", "pytest", nil},
		{"Makefile", "make", []string{"test"}},
	}

	for _, c := range checks {
		if fileExists(worktreePath, c.file) {
			return c.cmd, c.args
		}
	}
	return "", nil
}

func detectLintCommand(worktreePath string) (string, []string) {
	checks := []struct {
		file string
		cmd  string
		args []string
	}{
		{".golangci.yml", "golangci-lint", []string{"run"}},
		{"go.mod", "go", []string{"vet", "./..."}},
		{".eslintrc.js", "eslint", []string{"."}},
		{".eslintrc.json", "eslint", []string{"."}},
		{"pyproject.toml", "ruff", []string{"check", "."}},
	}

	for _, c := range checks {
		if fileExists(worktreePath, c.file) && isCommandAvailable(c.cmd) {
			return c.cmd, c.args
		}
	}
	return "", nil
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func isCommandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// parseCommand splits a config command string like "go test ./..." into a
// program and its arguments. It returns ("", nil) for an empty command so the
// caller can fall back to auto-detection.
func parseCommand(command string) (string, []string) {
	f := strings.Fields(command)
	if len(f) == 0 {
		return "", nil
	}
	return f[0], f[1:]
}

// withTimeout derives a context that cancels after d, or returns the parent
// unchanged (with a no-op cancel) when d <= 0.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// timeoutErr wraps err with a clearer message when the step's context expired.
func timeoutErr(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("timed out: %w", err)
	}
	return err
}

func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}
