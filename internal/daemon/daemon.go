package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jvrsantacruz/gitgate/internal/config"
	"github.com/jvrsantacruz/gitgate/internal/gitutil"
	"github.com/jvrsantacruz/gitgate/internal/pipeline"
	"github.com/jvrsantacruz/gitgate/internal/review"
)

const (
	// socketName is the Unix socket / named pipe used for IPC.
	socketName = "daemon.sock"

	// pidFileName tracks the daemon PID for detection.
	pidFileName = "daemon.pid"
)

// PushEvent is sent from the post-receive hook to the daemon.
type PushEvent struct {
	BareRepo string `json:"bare_repo"`
	Ref      string `json:"ref"`
	OldSHA   string `json:"old_sha"`
	NewSHA   string `json:"new_sha"`
}

// Daemon is the long-running GitGate background process.
type Daemon struct {
	home    string
	db      *DB
	logger  *slog.Logger
	queue   chan *Run
	wg      sync.WaitGroup
	workers int
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a new Daemon instance without starting it.
func New(home string) (*Daemon, error) {
	db, err := OpenDB(home)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	logPath := filepath.Join(home, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, cancel := context.WithCancel(context.Background())

	return &Daemon{
		home:    home,
		db:      db,
		logger:  logger,
		queue:   make(chan *Run, 64),
		workers: 2,
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

// Run starts the daemon and blocks until the context is cancelled.
func (d *Daemon) Run() error {
	defer d.cleanup()

	// Write PID file
	if err := writePIDFile(d.home); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer removePIDFile(d.home)

	d.logger.Info("daemon starting", "home", d.home, "workers", d.workers)

	// Recover from any previous crash
	if err := d.recoverFromCrash(); err != nil {
		d.logger.Warn("recovery had issues", "error", err)
	}

	// Start worker goroutines
	for i := range d.workers {
		d.wg.Add(1)
		go d.worker(i)
	}

	// Start IPC listener (Unix socket / named pipe)
	go d.listenIPC()

	d.logger.Info("daemon ready")

	// Wait for shutdown signal
	<-d.ctx.Done()

	d.logger.Info("daemon shutting down")
	close(d.queue)
	d.wg.Wait()

	d.logger.Info("daemon stopped")
	return nil
}

// Stop signals the daemon to stop.
func (d *Daemon) Stop() {
	d.cancel()
}

// worker processes pipeline runs from the queue.
func (d *Daemon) worker(id int) {
	defer d.wg.Done()

	d.logger.Info("worker started", "worker_id", id)

	for run := range d.queue {
		if err := d.processRun(run); err != nil {
			d.logger.Error("run failed", "run_id", run.ID, "error", err)
		}
	}

	d.logger.Info("worker stopped", "worker_id", id)
}

// processRun executes the pipeline for a single run.
func (d *Daemon) processRun(run *Run) error {
	d.logger.Info("processing run", "run_id", run.ID, "ref", run.Ref)

	// Update status to running
	run.Status = RunStatusRunning
	if err := d.db.UpdateRun(run); err != nil {
		return fmt.Errorf("failed to update run status: %w", err)
	}

	// Create worktree
	worktreePath := gitutil.WorktreePath(run.ID)
	run.WorktreePath = worktreePath

	if err := gitutil.CreateWorktree(run.BareRepo, worktreePath, run.NewSHA); err != nil {
		return d.failRun(run, fmt.Sprintf("failed to create worktree: %v", err))
	}

	defer func() {
		// Always clean up the worktree
		if err := gitutil.RemoveWorktree(run.BareRepo, worktreePath); err != nil {
			d.logger.Warn("failed to remove worktree", "run_id", run.ID, "error", err)
		}
	}()

	// Load bare repo config for origin remote info
	originURL, _ := gitutil.GetBareRepoConfig(run.BareRepo, "gitgate.origin-url")
	originRemote, _ := gitutil.GetBareRepoConfig(run.BareRepo, "gitgate.origin-remote")

	// Load the pushed repository's .gitgate.yml from the checked-out worktree.
	// Missing/invalid config falls back to sensible defaults.
	cfg := config.LoadOrDefault(filepath.Join(worktreePath, ".gitgate.yml"))

	// Merge any one-shot skip list stashed by `gitgate run --skip ...`.
	if extra, err := gitutil.GetBareRepoConfig(run.BareRepo, "gitgate.pending-skip"); err == nil && extra != "" {
		for _, s := range strings.Split(extra, ",") {
			if s = strings.TrimSpace(s); s != "" {
				cfg.Pipeline.Skip = append(cfg.Pipeline.Skip, s)
			}
		}
		_ = gitutil.UnsetBareRepoConfig(run.BareRepo, "gitgate.pending-skip")
	}

	// Build pipeline
	pipelineLogger := &dbLogger{db: d.db, runID: run.ID}
	pl := pipeline.New(pipeline.Config{
		WorktreePath: worktreePath,
		BareRepo:     run.BareRepo,
		Branch:       run.Branch,
		NewSHA:       run.NewSHA,
		Intent:       run.Intent,
		OriginURL:    originURL,
		OriginRemote: originRemote,
		SkipSteps:    cfg.Pipeline.Skip,
		TargetBranch: cfg.Pipeline.TargetBranch,
		TestCommand:  cfg.Test.Command,
		TestTimeout:  time.Duration(cfg.Test.TimeoutSecs) * time.Second,
		LintCommand:  cfg.Lint.Command,
		LintTimeout:  time.Duration(cfg.Lint.TimeoutSecs) * time.Second,
		Review: review.Options{
			Provider:     cfg.Review.Provider,
			Model:        cfg.Review.Model,
			CLICommand:   cfg.Review.CLICommand,
			MaxTokens:    cfg.Review.MaxTokens,
			SystemPrompt: cfg.Review.SystemPrompt,
			FailOn:       cfg.Review.FailOn,
		},
		Logger: pipelineLogger,
	})

	// Execute pipeline and track current step
	result, err := pl.Execute(d.ctx, func(step string) {
		run.CurrentStep = step
		_ = d.db.UpdateRun(run)
	})

	// Persist any review findings, whether or not the pipeline ultimately passed.
	if result != nil {
		d.persistFindings(run.ID, result)
	}

	if err != nil {
		return d.failRun(run, err.Error())
	}

	if result.NeedsUserInput {
		run.Status = RunStatusWaiting
		run.CurrentStep = result.WaitingStep
	} else if result.Success {
		run.Status = RunStatusSuccess
		run.CurrentStep = "done"
	} else {
		run.Status = RunStatusFailed
	}

	return d.db.UpdateRun(run)
}

// persistFindings stores every finding produced by the review step for a run.
func (d *Daemon) persistFindings(runID string, result *pipeline.Result) {
	for _, sr := range result.StepResults {
		for _, f := range sr.Findings {
			finding := &Finding{
				ID:          uuid.New().String(),
				RunID:       runID,
				Severity:    f.Severity,
				File:        f.File,
				Line:        f.Line,
				Description: f.Description,
				Action:      f.Action,
			}
			if err := d.db.AddFinding(finding); err != nil {
				d.logger.Warn("failed to persist finding", "run_id", runID, "error", err)
			}
		}
	}
}

// failRun marks a run as failed and logs the reason.
func (d *Daemon) failRun(run *Run, reason string) error {
	d.logger.Error("run failed", "run_id", run.ID, "reason", reason)
	_ = d.db.AddLog(run.ID, "error", run.CurrentStep, reason)
	run.Status = RunStatusFailed
	return d.db.UpdateRun(run)
}

// recoverFromCrash marks any runs that were active when the daemon last died as crashed,
// and prunes orphaned worktrees.
func (d *Daemon) recoverFromCrash() error {
	d.logger.Info("checking for crashed runs")

	if err := d.db.MarkCrashedRuns(); err != nil {
		return fmt.Errorf("marking crashed runs: %w", err)
	}

	// Prune orphaned worktrees
	worktreeBase := filepath.Join(d.home, "worktrees")
	entries, err := os.ReadDir(worktreeBase)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading worktrees directory: %w", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID := e.Name()
		run, err := d.db.GetRun(runID)
		if err != nil || (run != nil && run.Status != RunStatusRunning && run.Status != RunStatusPending) {
			// Orphaned or crashed - remove it
			path := filepath.Join(worktreeBase, runID)
			d.logger.Info("removing orphaned worktree", "path", path)
			_ = os.RemoveAll(path)
		}
	}

	return nil
}

// cleanup runs final cleanup tasks on daemon shutdown.
func (d *Daemon) cleanup() {
	d.db.Close()
}

// EnqueuePush creates a Run from a PushEvent and adds it to the processing queue.
func (d *Daemon) EnqueuePush(event PushEvent) error {
	branch := refToBranch(event.Ref)

	run := &Run{
		ID:       uuid.New().String(),
		BareRepo: event.BareRepo,
		Ref:      event.Ref,
		Branch:   branch,
		OldSHA:   event.OldSHA,
		NewSHA:   event.NewSHA,
	}

	// A `gitgate run --intent ...` invocation stashes the intent in the bare
	// repo config just before pushing; consume it here (one-shot).
	if intent, err := gitutil.GetBareRepoConfig(event.BareRepo, "gitgate.pending-intent"); err == nil && intent != "" {
		run.Intent = intent
		_ = gitutil.UnsetBareRepoConfig(event.BareRepo, "gitgate.pending-intent")
	}

	if err := d.db.CreateRun(run); err != nil {
		return fmt.Errorf("failed to create run: %w", err)
	}

	select {
	case d.queue <- run:
		d.logger.Info("enqueued run", "run_id", run.ID, "branch", branch)
	default:
		return fmt.Errorf("queue full; run %s not enqueued", run.ID)
	}

	return nil
}

// NotifyPush is called by the post-receive hook to send a push event to the daemon.
// It connects to the daemon's IPC socket and sends the event.
func NotifyPush(event PushEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal push event: %w", err)
	}
	return sendToSocket(gitutil.GitGateHome(), data)
}

// dbLogger adapts DB.AddLog to implement the pipeline.Logger interface.
type dbLogger struct {
	db    *DB
	runID string
}

func (l *dbLogger) Log(step, level, message string) {
	_ = l.db.AddLog(l.runID, level, step, message)
}

// refToBranch extracts the branch name from a full ref (e.g., refs/heads/main -> main).
func refToBranch(ref string) string {
	const prefix = "refs/heads/"
	if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
		return ref[len(prefix):]
	}
	return ref
}

// writePIDFile writes the current process PID to a file.
func writePIDFile(home string) error {
	pidPath := filepath.Join(home, pidFileName)
	return os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
}

// removePIDFile removes the PID file.
func removePIDFile(home string) {
	_ = os.Remove(filepath.Join(home, pidFileName))
}

// listenIPC starts the IPC listener (platform-specific implementation).
// This is a stub that will be implemented per-platform.
func (d *Daemon) listenIPC() {
	d.logger.Info("IPC listener started")
	// Platform-specific implementation is in ipc_unix.go and ipc_windows.go
	d.runIPCListener()
}

// keepaliveCheck periodically checks and logs daemon health.
func (d *Daemon) keepaliveCheck() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.logger.Info("daemon heartbeat", "queue_len", len(d.queue))
		}
	}
}
