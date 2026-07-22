package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jvrsantacruz/gitgate/internal/config"
	"github.com/jvrsantacruz/gitgate/internal/gitutil"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var (
		remote     string
		noStart    bool
		configFile string
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize GitGate for the current repository",
		Long: `Sets up a bare Git repository as a local quality gate, installs the
post-receive hook, and adds a 'gitgate' remote to your working repo.

After running this command, use:
  git push gitgate <branch>
instead of:
  git push origin <branch>`,
		Example: `  gitgate init
  gitgate init --remote upstream
  gitgate init --no-start`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(remote, noStart, configFile)
		},
	}

	cmd.Flags().StringVar(&remote, "remote", "origin", "Name of the real remote to forward pushes to")
	cmd.Flags().BoolVar(&noStart, "no-start", false, "Do not start the daemon after initialization")
	cmd.Flags().StringVar(&configFile, "config", ".gitgate.yml", "Config file to create/reference")

	return cmd
}

func runInit(remote string, noStart bool, cfgFile string) error {
	// Validate we're inside a Git repository
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	repoRoot, err := gitutil.FindRepoRoot(workDir)
	if err != nil {
		return fmt.Errorf("not inside a Git repository: %w", err)
	}

	fmt.Printf("📂 Repository root: %s\n", repoRoot)

	// Validate that the specified remote exists
	remoteURL, err := gitutil.GetRemoteURL(repoRoot, remote)
	if err != nil {
		return fmt.Errorf("remote %q not found: %w", remote, err)
	}

	fmt.Printf("🔗 Real remote (%s): %s\n", remote, remoteURL)

	// Check if gitgate remote already exists
	existingGGURL, _ := gitutil.GetRemoteURL(repoRoot, "gitgate")
	if existingGGURL != "" {
		return errors.New("gitgate remote already exists. Run 'gitgate init --reinit' to recreate it")
	}

	// Derive a unique ID for this repo (hash of real remote URL)
	repoID := gitutil.RepoID(remoteURL)

	// Create (or verify) the bare repo
	bareRepoPath := gitutil.BareRepoPath(repoID)
	fmt.Printf("🏗️  Setting up bare repo at: %s\n", bareRepoPath)

	if err := gitutil.EnsureBareRepo(bareRepoPath); err != nil {
		return fmt.Errorf("failed to create bare repo: %w", err)
	}

	// Install post-receive hook
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine gitgate binary path: %w", err)
	}

	fmt.Println("🪝  Installing post-receive hook...")
	if err := gitutil.InstallPostReceiveHook(bareRepoPath, selfPath); err != nil {
		return fmt.Errorf("failed to install hook: %w", err)
	}

	// Store metadata in the bare repo config
	if err := gitutil.SetBareRepoConfig(bareRepoPath, map[string]string{
		"gitgate.origin-remote": remote,
		"gitgate.origin-url":    remoteURL,
		"gitgate.repo-root":     repoRoot,
	}); err != nil {
		return fmt.Errorf("failed to store bare repo config: %w", err)
	}

	// Configure the real remote *inside* the bare repo so that pipeline
	// worktrees (which share the bare repo's config) can fetch, rebase, and
	// push against it. Without this the rebase/push steps have nowhere to go.
	if err := gitutil.EnsureRemote(bareRepoPath, remote, remoteURL); err != nil {
		return fmt.Errorf("failed to configure %q remote in bare repo: %w", remote, err)
	}

	// Add gitgate remote to the working repo
	fmt.Println("➕  Adding 'gitgate' remote...")
	if err := gitutil.AddRemote(repoRoot, "gitgate", bareRepoPath); err != nil {
		return fmt.Errorf("failed to add gitgate remote: %w", err)
	}

	// Create sample config file if not exists
	sampleCfgPath := filepath.Join(repoRoot, cfgFile)
	if _, err := os.Stat(sampleCfgPath); os.IsNotExist(err) {
		if writeErr := config.WriteSampleConfig(sampleCfgPath, remote); writeErr != nil {
			fmt.Printf("⚠️  Could not write sample config: %v\n", writeErr)
		} else {
			fmt.Printf("📝  Created sample config: %s\n", cfgFile)
		}
	}

	if !noStart {
		fmt.Println("🚀  Ensuring daemon is running...")
		// Best-effort: daemon start failures are not fatal
		if err := ensureDaemonRunning(); err != nil {
			fmt.Printf("⚠️  Could not start daemon: %v\n", err)
			fmt.Println("    Run 'gitgate daemon start' manually.")
		}
	}

	fmt.Printf(`
✅  GitGate initialized successfully!

Usage:
  git push gitgate <branch>   # Run quality gate pipeline
  git push origin <branch>    # Bypass gate (direct push)

Check status:
  gitgate status
  gitgate doctor
`)

	return nil
}
