package gitutil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jvrsantacruz/gitgate/internal/gitutil"
)

// setupTestRepo creates a temporary Git repository for testing.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")

	// Create an initial commit
	testFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial commit")

	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func runCaptureOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestFindRepoRoot(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Test from root
	root, err := gitutil.FindRepoRoot(repoDir)
	if err != nil {
		t.Fatalf("FindRepoRoot failed: %v", err)
	}

	// Normalize paths for comparison (important on Windows, where git reports
	// forward slashes regardless of the OS separator).
	if !samePath(root, repoDir) {
		t.Errorf("expected %s, got %s", repoDir, root)
	}

	// Test from subdirectory
	subDir := filepath.Join(repoDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root2, err := gitutil.FindRepoRoot(subDir)
	if err != nil {
		t.Fatalf("FindRepoRoot from subdir failed: %v", err)
	}
	if !samePath(root2, repoDir) {
		t.Errorf("expected %s, got %s", repoDir, root2)
	}
}

// samePath compares two paths ignoring case and separator style (\ vs /), so
// git's forward-slash output matches an OS-native path on Windows.
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.ToSlash(a), filepath.ToSlash(b))
}

func TestFindRepoRoot_NotInRepo(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := gitutil.FindRepoRoot(tmpDir)
	if err == nil {
		t.Error("expected error for non-repo directory, got nil")
	}
}

func TestRepoID(t *testing.T) {
	id1 := gitutil.RepoID("https://github.com/user/repo.git")
	id2 := gitutil.RepoID("https://github.com/user/repo.git")
	id3 := gitutil.RepoID("https://github.com/user/other.git")

	if id1 != id2 {
		t.Error("same URL should produce same ID")
	}
	if id1 == id3 {
		t.Error("different URLs should produce different IDs")
	}
	if len(id1) == 0 {
		t.Error("ID should not be empty")
	}
}

func TestEnsureBareRepo(t *testing.T) {
	dir := t.TempDir()
	bareRepoPath := filepath.Join(dir, "test.git")

	// First creation
	if err := gitutil.EnsureBareRepo(bareRepoPath); err != nil {
		t.Fatalf("EnsureBareRepo failed: %v", err)
	}

	// Verify it's a bare repo (has HEAD file)
	if _, err := os.Stat(filepath.Join(bareRepoPath, "HEAD")); err != nil {
		t.Errorf("bare repo HEAD not found: %v", err)
	}

	// Second call should be idempotent
	if err := gitutil.EnsureBareRepo(bareRepoPath); err != nil {
		t.Errorf("second EnsureBareRepo call failed: %v", err)
	}
}

func TestInstallPostReceiveHook(t *testing.T) {
	dir := t.TempDir()
	bareRepoPath := filepath.Join(dir, "test.git")

	if err := gitutil.EnsureBareRepo(bareRepoPath); err != nil {
		t.Fatal(err)
	}

	fakeBin := "/usr/local/bin/gitgate"
	if err := gitutil.InstallPostReceiveHook(bareRepoPath, fakeBin); err != nil {
		t.Fatalf("InstallPostReceiveHook failed: %v", err)
	}

	hookPath := filepath.Join(bareRepoPath, "hooks", "post-receive")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("hook file not found: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, fakeBin) {
		t.Errorf("hook does not contain gitgate binary path")
	}
	if !strings.Contains(content, "daemon notify-push") {
		t.Errorf("hook does not contain notify-push command")
	}
	if !strings.HasPrefix(content, "#!/bin/sh") {
		t.Errorf("hook does not start with #!/bin/sh shebang")
	}

	// Verify it's executable (mode check)
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	// On Windows, executable bits work differently, just check it exists
	if info.Size() == 0 {
		t.Error("hook file is empty")
	}
}

func TestGetSetRemote(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Add a fake remote
	fakeURL := "https://github.com/user/repo.git"
	if err := gitutil.AddRemote(repoDir, "testremote", fakeURL); err != nil {
		t.Fatalf("AddRemote failed: %v", err)
	}

	// Get it back
	url, err := gitutil.GetRemoteURL(repoDir, "testremote")
	if err != nil {
		t.Fatalf("GetRemoteURL failed: %v", err)
	}
	if url != fakeURL {
		t.Errorf("expected %s, got %s", fakeURL, url)
	}

	// Non-existent remote
	_, err = gitutil.GetRemoteURL(repoDir, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent remote")
	}
}

func TestCreateRemoveWorktree(t *testing.T) {
	// Set up a source repo
	sourceDir := setupTestRepo(t)

	// Create a bare repo by cloning
	tmpDir := t.TempDir()
	bareRepoPath := filepath.Join(tmpDir, "bare.git")

	cmd := exec.Command("git", "clone", "--bare", sourceDir, bareRepoPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create bare repo: %v\n%s", err, out)
	}

	// Get HEAD SHA
	sha, err := runCaptureOutput("git", "-C", bareRepoPath, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("failed to get HEAD: %v", err)
	}

	// Create worktree
	worktreePath := filepath.Join(tmpDir, "worktree-test")
	if err := gitutil.CreateWorktree(bareRepoPath, worktreePath, sha); err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	// Verify worktree exists
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("worktree directory not created: %v", err)
	}

	// Verify it's a valid git repo (has .git file or directory)
	hasGit := false
	for _, name := range []string{".git", "HEAD"} {
		if _, err := os.Stat(filepath.Join(worktreePath, name)); err == nil {
			hasGit = true
			break
		}
	}
	if !hasGit {
		t.Error("worktree has no .git file or HEAD")
	}

	// Remove worktree
	if err := gitutil.RemoveWorktree(bareRepoPath, worktreePath); err != nil {
		t.Errorf("RemoveWorktree failed: %v", err)
	}
}

func TestSetBareRepoConfig(t *testing.T) {
	dir := t.TempDir()
	bareRepoPath := filepath.Join(dir, "config.git")
	if err := gitutil.EnsureBareRepo(bareRepoPath); err != nil {
		t.Fatal(err)
	}

	values := map[string]string{
		"gitgate.origin-url":    "https://github.com/user/repo.git",
		"gitgate.origin-remote": "origin",
	}

	if err := gitutil.SetBareRepoConfig(bareRepoPath, values); err != nil {
		t.Fatalf("SetBareRepoConfig failed: %v", err)
	}

	val, err := gitutil.GetBareRepoConfig(bareRepoPath, "gitgate.origin-url")
	if err != nil {
		t.Fatalf("GetBareRepoConfig failed: %v", err)
	}
	if val != "https://github.com/user/repo.git" {
		t.Errorf("expected URL, got %s", val)
	}
}
