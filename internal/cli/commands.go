package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jvrsantacruz/gitgate/internal/config"
	"github.com/jvrsantacruz/gitgate/internal/daemon"
	"github.com/jvrsantacruz/gitgate/internal/gitutil"
	"github.com/spf13/cobra"
)

// runPollTimeout caps how long `gitgate run` waits for a pipeline to finish.
const runPollTimeout = 30 * time.Minute

func newStatusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show pipeline run status",
		Long:  `Display the status of all recent pipeline runs.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(jsonOutput)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

func runStatus(jsonOutput bool) error {
	db, err := daemon.OpenDB(gitutil.GitGateHome())
	if err != nil {
		return fmt.Errorf("cannot open database: %w", err)
	}
	defer db.Close()

	runs, err := db.ListRuns(20)
	if err != nil {
		return fmt.Errorf("failed to list runs: %w", err)
	}

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(runs)
	}

	if len(runs) == 0 {
		fmt.Println("No pipeline runs found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tSTEP\tBRANCH\tSTARTED")
	for _, r := range runs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.ID[:8],
			r.Status,
			r.CurrentStep,
			r.Branch,
			r.CreatedAt.Format(time.RFC3339),
		)
	}
	return w.Flush()
}

func newRunCmd() *cobra.Command {
	var (
		intent     string
		skip       []string
		jsonOutput bool
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "run [branch]",
		Short: "Manually trigger a pipeline run",
		Long: `Manually trigger the GitGate pipeline for a branch.
This is useful for automation/agent workflows where you want JSON output.`,
		Example: `  gitgate run main --intent "fix: resolve race condition"
  gitgate run --json --intent "feat: add user auth"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) > 0 {
				branch = args[0]
			}
			return runPipeline(branch, intent, skip, jsonOutput, dryRun)
		},
	}

	cmd.Flags().StringVar(&intent, "intent", "", "Intent/description of the changes")
	cmd.Flags().StringSliceVar(&skip, "skip", nil, "Steps to skip (e.g. --skip lint,test)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Stream results as JSON (for agent use)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate config without running pipeline")

	return cmd
}

// runOutput is the JSON payload emitted by `gitgate run --json`.
type runOutput struct {
	Run      *daemon.Run       `json:"run"`
	Findings []*daemon.Finding `json:"findings"`
}

func runPipeline(branch, intent string, skip []string, jsonOutput, dryRun bool) error {
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}
	repoRoot, err := gitutil.FindRepoRoot(workDir)
	if err != nil {
		return fmt.Errorf("not inside a Git repository: %w", err)
	}

	if branch == "" {
		branch, err = gitutil.GetCurrentBranch(repoRoot)
		if err != nil {
			return fmt.Errorf("cannot determine current branch: %w", err)
		}
	}

	cfg := config.LoadOrDefault(filepath.Join(repoRoot, ".gitgate.yml"))

	if dryRun {
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"valid":         true,
				"branch":        branch,
				"intent":        intent,
				"skip":          skip,
				"target_branch": cfg.Pipeline.TargetBranch,
				"review":        cfg.Review.Provider,
			})
		}
		fmt.Printf("✅ Config valid. Would run pipeline on branch %q (review: %s, target: %s).\n",
			branch, cfg.Review.Provider, cfg.Pipeline.TargetBranch)
		return nil
	}

	// The gitgate remote must exist and point at the bare repo.
	bareRepo, err := gitutil.GetRemoteURL(repoRoot, "gitgate")
	if err != nil {
		return fmt.Errorf("gitgate remote not found; run 'gitgate init' first")
	}

	if running, _ := isDaemonRunning(); !running {
		return fmt.Errorf("daemon is not running; start it with 'gitgate daemon start'")
	}

	sha, err := gitutil.RevParse(repoRoot, branch)
	if err != nil {
		return fmt.Errorf("cannot resolve branch %q: %w", branch, err)
	}

	// Stash intent/skip as one-shot values the daemon consumes for this push.
	if intent != "" {
		if err := gitutil.SetBareRepoConfig(bareRepo, map[string]string{"gitgate.pending-intent": intent}); err != nil {
			return fmt.Errorf("failed to record intent: %w", err)
		}
	}
	if len(skip) > 0 {
		if err := gitutil.SetBareRepoConfig(bareRepo, map[string]string{"gitgate.pending-skip": strings.Join(skip, ",")}); err != nil {
			return fmt.Errorf("failed to record skip list: %w", err)
		}
	}

	// Record the push time so we can identify the run this push creates.
	since := time.Now().Add(-2 * time.Second)

	if !jsonOutput {
		fmt.Printf("🚀 Triggering pipeline for %q...\n", branch)
	}
	if out, err := gitutil.PushToRemote(repoRoot, "gitgate", branch); err != nil {
		return fmt.Errorf("push to gitgate failed: %w\n%s", err, out)
	}

	db, err := daemon.OpenDB(gitutil.GitGateHome())
	if err != nil {
		return fmt.Errorf("cannot open database: %w", err)
	}
	defer db.Close()

	run, err := waitForRun(db, sha, since, jsonOutput)
	if err != nil {
		return err
	}

	findings, _ := db.GetFindings(run.ID)

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(runOutput{Run: run, Findings: findings})
	}
	printRunSummary(run, findings)
	if run.Status != daemon.RunStatusSuccess {
		return fmt.Errorf("pipeline did not succeed (status: %s)", run.Status)
	}
	return nil
}

// waitForRun polls the database for the run created by our push (matched by SHA
// and creation time) and blocks until it reaches a terminal state.
func waitForRun(db *daemon.DB, sha string, since time.Time, quiet bool) (*daemon.Run, error) {
	deadline := time.Now().Add(runPollTimeout)
	var lastStep string

	for time.Now().Before(deadline) {
		run := findRunBySHA(db, sha, since)
		if run != nil {
			if !quiet && run.CurrentStep != lastStep && run.CurrentStep != "" {
				fmt.Printf("   → %s\n", run.CurrentStep)
				lastStep = run.CurrentStep
			}
			if isTerminal(run.Status) {
				return run, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out after %s waiting for pipeline to finish", runPollTimeout)
}

// findRunBySHA returns the most recent run whose NewSHA matches and that was
// created at or after since.
func findRunBySHA(db *daemon.DB, sha string, since time.Time) *daemon.Run {
	runs, err := db.ListRuns(50)
	if err != nil {
		return nil
	}
	for _, r := range runs {
		if r.NewSHA == sha && !r.CreatedAt.Before(since) {
			return r
		}
	}
	return nil
}

// isTerminal reports whether a run status will not change further on its own.
// "waiting" is included: the run has stopped and needs `gitgate respond`.
func isTerminal(s daemon.RunStatus) bool {
	switch s {
	case daemon.RunStatusSuccess, daemon.RunStatusFailed,
		daemon.RunStatusAborted, daemon.RunStatusCrash, daemon.RunStatusWaiting:
		return true
	default:
		return false
	}
}

func printRunSummary(run *daemon.Run, findings []*daemon.Finding) {
	icon := "❌"
	switch run.Status {
	case daemon.RunStatusSuccess:
		icon = "✅"
	case daemon.RunStatusWaiting:
		icon = "⏸️"
	}
	fmt.Printf("\n%s Run %s — %s (step: %s)\n", icon, run.ID[:8], run.Status, run.CurrentStep)
	if len(findings) > 0 {
		fmt.Printf("\n%d finding(s):\n", len(findings))
		for _, f := range findings {
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Printf("  [%s] %s — %s\n", strings.ToUpper(f.Severity), loc, f.Description)
		}
	}
}

func newRespondCmd() *cobra.Command {
	var (
		runID      string
		findingID  string
		action     string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "respond",
		Short: "Respond to a pipeline finding that requires user input",
		Long: `When a pipeline run is paused waiting for user input (e.g., an AI review finding
with action 'ask-user'), use this command to approve, fix, or skip it.

This command is designed for automation/agent workflows (outputs JSON).`,
		Example: `  gitgate respond --id <run-id> --finding <finding-id> --action fix
  gitgate respond --id <run-id> --finding <finding-id> --action skip
  gitgate respond --id <run-id> --finding <finding-id> --action abort`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRespond(runID, findingID, action, jsonOutput)
		},
	}

	cmd.Flags().StringVar(&runID, "id", "", "Run ID to respond to")
	cmd.Flags().StringVar(&findingID, "finding", "", "Finding ID to respond to")
	cmd.Flags().StringVar(&action, "action", "", "Action: fix, skip, abort")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("action")

	return cmd
}

func runRespond(runID, findingID, action string, jsonOutput bool) error {
	db, err := daemon.OpenDB(gitutil.GitGateHome())
	if err != nil {
		return fmt.Errorf("cannot open database: %w", err)
	}
	defer db.Close()

	run, err := resolveRun(db, runID)
	if err != nil {
		return err
	}

	emit := func(payload map[string]any) error {
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(payload)
		}
		fmt.Printf("✅ %v\n", payload["message"])
		return nil
	}

	switch strings.ToLower(action) {
	case "abort":
		run.Status = daemon.RunStatusAborted
		if err := db.UpdateRun(run); err != nil {
			return fmt.Errorf("failed to abort run: %w", err)
		}
		return emit(map[string]any{
			"run_id":  run.ID,
			"status":  string(run.Status),
			"message": fmt.Sprintf("run %s aborted", run.ID[:8]),
		})

	case "fix", "skip":
		if findingID == "" {
			return fmt.Errorf("--finding is required for action %q", action)
		}
		finding, err := resolveFinding(db, run.ID, findingID)
		if err != nil {
			return err
		}
		newStatus := "skipped"
		if action == "fix" {
			newStatus = "applied"
		}
		if err := db.UpdateFindingStatus(finding.ID, newStatus); err != nil {
			return fmt.Errorf("failed to update finding: %w", err)
		}
		return emit(map[string]any{
			"run_id":     run.ID,
			"finding_id": finding.ID,
			"status":     newStatus,
			"message":    fmt.Sprintf("finding %s marked %s", finding.ID[:8], newStatus),
		})

	default:
		return fmt.Errorf("unknown action %q (expected: fix, skip, abort)", action)
	}
}

// resolveRun finds a run by full ID or unambiguous short prefix.
func resolveRun(db *daemon.DB, idOrPrefix string) (*daemon.Run, error) {
	if run, err := db.GetRun(idOrPrefix); err == nil && run != nil {
		return run, nil
	}
	runs, err := db.ListRuns(200)
	if err != nil {
		return nil, err
	}
	var match *daemon.Run
	for _, r := range runs {
		if strings.HasPrefix(r.ID, idOrPrefix) {
			if match != nil {
				return nil, fmt.Errorf("run id prefix %q is ambiguous", idOrPrefix)
			}
			match = r
		}
	}
	if match == nil {
		return nil, fmt.Errorf("no run found matching %q", idOrPrefix)
	}
	return match, nil
}

// resolveFinding finds a finding within a run by full ID or short prefix.
func resolveFinding(db *daemon.DB, runID, idOrPrefix string) (*daemon.Finding, error) {
	findings, err := db.GetFindings(runID)
	if err != nil {
		return nil, err
	}
	var match *daemon.Finding
	for _, f := range findings {
		if f.ID == idOrPrefix || strings.HasPrefix(f.ID, idOrPrefix) {
			if match != nil {
				return nil, fmt.Errorf("finding id prefix %q is ambiguous", idOrPrefix)
			}
			match = f
		}
	}
	if match == nil {
		return nil, fmt.Errorf("no finding %q in run %s", idOrPrefix, runID[:8])
	}
	return match, nil
}

func newUpdateCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update GitGate to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force update even if runs are in progress")
	return cmd
}
