package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/jvrsantacruz/gitgate/internal/gitutil"
	"github.com/spf13/cobra"
)

// Check represents a single doctor check result.
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "fail"
	Message string `json:"message"`
}

func newDoctorCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system dependencies and GitGate configuration",
		Long:  `Verifies that all required tools are installed and configured correctly.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(jsonOutput)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

func runDoctor(jsonOutput bool) error {
	checks := []Check{}

	// 1. Check git
	checks = append(checks, checkBinary("git", "git --version"))

	// 2. Check go (optional, for development)
	checks = append(checks, checkBinary("go", "go version"))

	// 3. Check gh (GitHub CLI)
	checks = append(checks, checkBinaryOptional("gh", "gh --version", "Required for GitHub PR creation"))

	// 4. Check glab (GitLab CLI)
	checks = append(checks, checkBinaryOptional("glab", "glab --version", "Required for GitLab MR creation"))

	// 5. Check OS service manager
	checks = append(checks, checkServiceManager())

	// 6. Check gitgate home directory
	checks = append(checks, checkGitGateHome())

	// 7. Check if we're in a git repo with gitgate configured
	checks = append(checks, checkCurrentRepo())

	// 8. Check daemon status
	checks = append(checks, checkDaemonStatus())

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(checks)
	}

	// Pretty print
	allOK := true
	for _, c := range checks {
		icon := "✅"
		if c.Status == "warn" {
			icon = "⚠️ "
		} else if c.Status == "fail" {
			icon = "❌"
			allOK = false
		}
		fmt.Printf("%s  %-20s %s\n", icon, c.Name, c.Message)
	}

	if !allOK {
		fmt.Println("\nSome checks failed. Run 'gitgate init' to set up the repository.")
		return fmt.Errorf("doctor found issues")
	}

	fmt.Println("\nAll checks passed! GitGate is ready.")
	return nil
}

func checkBinary(name, versionCmd string) Check {
	parts := strings.Fields(versionCmd)
	out, err := runCaptured(parts[0], parts[1:]...)
	if err != nil {
		return Check{name, "fail", fmt.Sprintf("not found: install %s", name)}
	}
	version := strings.TrimSpace(strings.Split(out, "\n")[0])
	return Check{name, "ok", version}
}

func checkBinaryOptional(name, versionCmd, hint string) Check {
	parts := strings.Fields(versionCmd)
	out, err := runCaptured(parts[0], parts[1:]...)
	if err != nil {
		return Check{name, "warn", fmt.Sprintf("not found (%s)", hint)}
	}
	version := strings.TrimSpace(strings.Split(out, "\n")[0])
	return Check{name, "ok", version}
}

func checkServiceManager() Check {
	switch runtime.GOOS {
	case "windows":
		return Check{"service-manager", "ok", "Windows Task Scheduler / Service"}
	case "darwin":
		return Check{"service-manager", "ok", "launchd"}
	case "linux":
		_, err := runCaptured("systemctl", "--version")
		if err == nil {
			return Check{"service-manager", "ok", "systemd"}
		}
		return Check{"service-manager", "warn", "systemd not found; daemon will run as detached process"}
	default:
		return Check{"service-manager", "warn", fmt.Sprintf("unknown OS %s; daemon will run as detached process", runtime.GOOS)}
	}
}

func checkGitGateHome() Check {
	home := gitutil.GitGateHome()
	if _, err := os.Stat(home); os.IsNotExist(err) {
		return Check{"gitgate-home", "warn", fmt.Sprintf("directory does not exist yet: %s", home)}
	}
	return Check{"gitgate-home", "ok", home}
}

func checkCurrentRepo() Check {
	wd, err := os.Getwd()
	if err != nil {
		return Check{"current-repo", "fail", "cannot determine working directory"}
	}

	root, err := gitutil.FindRepoRoot(wd)
	if err != nil {
		return Check{"current-repo", "warn", "not inside a Git repository"}
	}

	_, err = gitutil.GetRemoteURL(root, "gitgate")
	if err != nil {
		return Check{"current-repo", "warn", "gitgate remote not configured (run 'gitgate init')"}
	}

	return Check{"current-repo", "ok", fmt.Sprintf("configured at %s", root)}
}

func checkDaemonStatus() Check {
	running, err := isDaemonRunning()
	if err != nil {
		return Check{"daemon", "warn", fmt.Sprintf("cannot check status: %v", err)}
	}
	if !running {
		return Check{"daemon", "warn", "daemon not running (run 'gitgate daemon start')"}
	}
	return Check{"daemon", "ok", "running"}
}
