// Package gitutil provides helpers for interacting with Git repositories.
package gitutil

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GitGateHome returns the path to the ~/.gitgate directory.
func GitGateHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("cannot determine home directory: %v", err))
	}
	return filepath.Join(home, ".gitgate")
}

// BareRepoPath returns the path where the bare repo for a given repo ID should live.
func BareRepoPath(repoID string) string {
	return filepath.Join(GitGateHome(), "repos", repoID+".git")
}

// WorktreePath returns the path where a run's worktree should live.
func WorktreePath(runID string) string {
	return filepath.Join(GitGateHome(), "worktrees", runID)
}

// RepoID returns a deterministic short ID for a repo based on its remote URL.
func RepoID(remoteURL string) string {
	h := sha256.Sum256([]byte(remoteURL))
	return fmt.Sprintf("%x", h[:8])
}

// FindRepoRoot finds the root directory of the Git repository containing dir.
func FindRepoRoot(dir string) (string, error) {
	out, err := runGitInDir(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// GetRemoteURL returns the URL for a named remote.
func GetRemoteURL(repoRoot, remoteName string) (string, error) {
	out, err := runGitInDir(repoRoot, "remote", "get-url", remoteName)
	if err != nil {
		return "", fmt.Errorf("remote %q not found", remoteName)
	}
	return strings.TrimSpace(out), nil
}

// AddRemote adds a named remote to the repository at repoRoot.
func AddRemote(repoRoot, name, url string) error {
	_, err := runGitInDir(repoRoot, "remote", "add", name, url)
	return err
}

// RemoveRemote removes a named remote from the repository.
func RemoveRemote(repoRoot, name string) error {
	_, err := runGitInDir(repoRoot, "remote", "remove", name)
	return err
}

// EnsureRemote adds a named remote pointing at url, or updates its URL if the
// remote already exists. Safe to call repeatedly (e.g. on re-init).
func EnsureRemote(repoRoot, name, url string) error {
	if _, err := runGitInDir(repoRoot, "remote", "add", name, url); err == nil {
		return nil
	}
	// Already exists: make sure the URL is current.
	_, err := runGitInDir(repoRoot, "remote", "set-url", name, url)
	return err
}

// EnsureBareRepo creates a bare Git repo at path if it doesn't exist.
func EnsureBareRepo(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Check if it's already initialized
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		return nil // Already a bare repo
	}

	_, err := runGit("init", "--bare", path)
	return err
}

// InstallPostReceiveHook installs the post-receive hook in a bare repo.
// The hook calls "gitgate daemon notify-push" with the push details.
func InstallPostReceiveHook(bareRepoPath, gitgateBin string) error {
	hookDir := filepath.Join(bareRepoPath, "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}

	hookPath := filepath.Join(hookDir, "post-receive")
	content := buildPostReceiveHook(gitgateBin)

	if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
		return fmt.Errorf("failed to write hook: %w", err)
	}

	return nil
}

// buildPostReceiveHook generates the post-receive hook script content.
func buildPostReceiveHook(gitgateBin string) string {
	// On Windows we need a slightly different shebang/invocation approach.
	// Git for Windows can run shell scripts, so we use a POSIX-style hook.
	escapedBin := strings.ReplaceAll(gitgateBin, `\`, `/`)

	return fmt.Sprintf(`#!/bin/sh
# GitGate post-receive hook
# This hook notifies the GitGate daemon of new pushes.
# DO NOT EDIT - managed by gitgate

GITGATE_BIN="%s"
BARE_REPO="$(pwd)"

while read OLD_SHA NEW_SHA REF; do
    # Notify daemon (non-blocking, errors are ignored)
    "$GITGATE_BIN" daemon notify-push \
        --bare-repo "$BARE_REPO" \
        --ref "$REF" \
        --old-sha "$OLD_SHA" \
        --new-sha "$NEW_SHA" &>/dev/null &
done

# Always exit 0 so the push is never blocked by hook failures
exit 0
`, escapedBin)
}

// SetBareRepoConfig stores key-value pairs in the bare repo's git config.
func SetBareRepoConfig(bareRepoPath string, values map[string]string) error {
	for key, val := range values {
		if _, err := runGitInDir(bareRepoPath, "config", key, val); err != nil {
			return fmt.Errorf("failed to set config %s: %w", key, err)
		}
	}
	return nil
}

// GetBareRepoConfig retrieves a value from the bare repo's git config.
func GetBareRepoConfig(bareRepoPath, key string) (string, error) {
	out, err := runGitInDir(bareRepoPath, "config", "--get", key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// UnsetBareRepoConfig removes a key from the bare repo's git config. It is a
// no-op (nil error) if the key does not exist.
func UnsetBareRepoConfig(bareRepoPath, key string) error {
	// git config --unset returns exit code 5 when the key is missing; ignore it.
	_, _ = runGitInDir(bareRepoPath, "config", "--unset", key)
	return nil
}

// CreateWorktree creates a git worktree for a specific SHA at the given path.
func CreateWorktree(bareRepoPath, worktreePath, sha string) error {
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return err
	}
	_, err := runGitInDir(bareRepoPath, "worktree", "add", "--detach", worktreePath, sha)
	return err
}

// RemoveWorktree removes a git worktree and its directory.
func RemoveWorktree(bareRepoPath, worktreePath string) error {
	// Force remove in case of unclean state
	_, err := runGitInDir(bareRepoPath, "worktree", "remove", "--force", worktreePath)
	if err != nil {
		// If git worktree remove fails, try to manually remove the directory
		_ = os.RemoveAll(worktreePath)
	}
	return nil
}

// ListWorktrees returns a list of active worktrees for a bare repo.
func ListWorktrees(bareRepoPath string) ([]string, error) {
	out, err := runGitInDir(bareRepoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}

// PruneWorktrees removes stale worktree references.
func PruneWorktrees(bareRepoPath string) error {
	_, err := runGitInDir(bareRepoPath, "worktree", "prune")
	return err
}

// GetCurrentBranch returns the current branch name in a working directory.
func GetCurrentBranch(dir string) (string, error) {
	out, err := runGitInDir(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RevParse resolves a revision (branch, tag, or ref) to its full commit SHA.
func RevParse(dir, rev string) (string, error) {
	out, err := runGitInDir(dir, "rev-parse", "--verify", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// PushToRemote pushes a branch to the named remote from the repo at dir. It
// returns the combined git output so callers can surface push errors verbatim.
func PushToRemote(dir, remote, branch string) (string, error) {
	cmd := exec.Command("git", "push", remote, branch)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// IsWindowsGitPath detects if running on Windows and adjusts path separators.
func IsWindowsGitPath() bool {
	return runtime.GOOS == "windows"
}

// runGit runs a git command in the current directory.
func runGit(args ...string) (string, error) {
	return runGitInDir("", args...)
}

// runGitInDir runs a git command in the specified directory.
func runGitInDir(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), errMsg)
	}

	return stdout.String(), nil
}
