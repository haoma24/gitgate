package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/jvrsantacruz/gitgate/internal/daemon"
	"github.com/jvrsantacruz/gitgate/internal/gitutil"
)

// releaseAPI is the GitHub endpoint for the latest published release.
const releaseAPI = "https://api.github.com/repos/jvrsantacruz/gitgate/releases/latest"

// ghRelease is the subset of the GitHub release payload we consume.
type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// assetName returns the release asset name for the running platform, matching
// the artifact names produced by `make build-all`.
func assetName() string {
	name := fmt.Sprintf("gitgate-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func runUpdate(force bool) error {
	// Refuse to swap the binary out from under active pipeline runs.
	if !force {
		if db, err := daemon.OpenDB(gitutil.GitGateHome()); err == nil {
			active, _ := db.ListActiveRuns()
			db.Close()
			if len(active) > 0 {
				return fmt.Errorf("%d run(s) in progress; wait for them or use --force", len(active))
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("🔎 Checking for the latest release...")
	release, err := fetchLatestRelease(ctx)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	want := assetName()
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == want {
			downloadURL = a.URL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("release %s has no asset %q for this platform", release.TagName, want)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate current executable: %w", err)
	}
	self, _ = filepath.EvalSymlinks(self)

	fmt.Printf("⬇️  Downloading %s (%s)...\n", release.TagName, want)
	newBinary := self + ".new"
	if err := downloadTo(ctx, downloadURL, newBinary); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(newBinary) // no-op once renamed into place

	if err := os.Chmod(newBinary, 0o755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := replaceExecutable(self, newBinary); err != nil {
		return fmt.Errorf("failed to install update: %w", err)
	}

	fmt.Printf("✅ Updated to %s. Restart the daemon to run the new version:\n", release.TagName)
	fmt.Println("   gitgate daemon restart")
	return nil
}

func fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("no published release found")
	}
	return &release, nil
}

func downloadTo(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return f.Close()
}

// replaceExecutable atomically swaps the running binary for the downloaded one.
// A running executable cannot be deleted on Windows, but it can be renamed, so
// we move the old binary aside first and roll back if the swap fails.
func replaceExecutable(current, replacement string) error {
	backup := current + ".old"
	_ = os.Remove(backup)

	if err := os.Rename(current, backup); err != nil {
		return fmt.Errorf("could not move current binary aside: %w", err)
	}
	if err := os.Rename(replacement, current); err != nil {
		// Roll back so the user is not left without a binary.
		_ = os.Rename(backup, current)
		return err
	}

	// Best-effort cleanup; on Windows the old binary may linger until reboot.
	_ = os.Remove(backup)
	return nil
}
