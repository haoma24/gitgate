package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jvrsantacruz/gitgate/internal/daemon"
	"github.com/jvrsantacruz/gitgate/internal/gitutil"
	"github.com/spf13/cobra"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the GitGate background daemon",
	}

	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonRestartCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	cmd.AddCommand(newDaemonNotifyPushCmd())
	cmd.AddCommand(newDaemonLogsCmd())

	return cmd
}

func newDaemonStartCmd() *cobra.Command {
	var foreground bool

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the GitGate daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemonStart(foreground)
		},
	}

	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run daemon in foreground (do not detach)")
	return cmd
}

func runDaemonStart(foreground bool) error {
	running, err := isDaemonRunning()
	if err == nil && running {
		fmt.Println("Daemon is already running.")
		return nil
	}

	if foreground {
		fmt.Println("Starting daemon in foreground...")
		d, err := daemon.New(gitutil.GitGateHome())
		if err != nil {
			return fmt.Errorf("failed to initialize daemon: %w", err)
		}
		return d.Run()
	}

	fmt.Println("Starting GitGate daemon...")
	mgr, err := daemon.NewServiceManager()
	if err != nil {
		return fmt.Errorf("failed to initialize service manager: %w", err)
	}

	if err := mgr.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	fmt.Println("✅ Daemon started successfully.")
	return nil
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the GitGate daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := daemon.NewServiceManager()
			if err != nil {
				return err
			}
			if err := mgr.Stop(); err != nil {
				return fmt.Errorf("failed to stop daemon: %w", err)
			}
			fmt.Println("✅ Daemon stopped.")
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the GitGate daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := daemon.NewServiceManager()
			if err != nil {
				return err
			}
			if err := mgr.Restart(); err != nil {
				return fmt.Errorf("failed to restart daemon: %w", err)
			}
			fmt.Println("✅ Daemon restarted.")
			return nil
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			running, err := isDaemonRunning()
			if err != nil {
				fmt.Printf("❓ Status unknown: %v\n", err)
				return nil
			}
			if running {
				fmt.Println("✅ Daemon is running.")
			} else {
				fmt.Println("❌ Daemon is not running.")
			}
			return nil
		},
	}
}

// newDaemonNotifyPushCmd is the internal command called by the post-receive hook.
func newDaemonNotifyPushCmd() *cobra.Command {
	var (
		bareRepo string
		ref      string
		oldSHA   string
		newSHA   string
	)

	cmd := &cobra.Command{
		Use:    "notify-push",
		Hidden: true, // Internal use only
		Short:  "Notify daemon of a new push (called by post-receive hook)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.NotifyPush(daemon.PushEvent{
				BareRepo: bareRepo,
				Ref:      ref,
				OldSHA:   oldSHA,
				NewSHA:   newSHA,
			})
		},
	}

	cmd.Flags().StringVar(&bareRepo, "bare-repo", "", "Path to bare repository")
	cmd.Flags().StringVar(&ref, "ref", "", "Git ref being pushed")
	cmd.Flags().StringVar(&oldSHA, "old-sha", "", "Previous SHA")
	cmd.Flags().StringVar(&newSHA, "new-sha", "", "New SHA")
	_ = cmd.MarkFlagRequired("bare-repo")
	_ = cmd.MarkFlagRequired("ref")

	return cmd
}

func newDaemonLogsCmd() *cobra.Command {
	var (
		runID string
		lines int
	)

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show daemon or run logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showDaemonLogs(runID, lines)
		},
	}

	cmd.Flags().StringVar(&runID, "run", "", "Show logs for specific run ID")
	cmd.Flags().IntVar(&lines, "tail", 50, "Number of lines to show")
	return cmd
}

func showDaemonLogs(runID string, lines int) error {
	db, err := daemon.OpenDB(gitutil.GitGateHome())
	if err != nil {
		return fmt.Errorf("cannot open database: %w", err)
	}
	defer db.Close()

	logs, err := db.GetLogs(runID, lines)
	if err != nil {
		return fmt.Errorf("failed to retrieve logs: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tLEVEL\tMESSAGE")
	for _, l := range logs {
		// Messages may be multi-line (e.g. captured git/test output). Print the
		// first line in the table row, then continuation lines indented under the
		// MESSAGE column so the tabwriter alignment is preserved and nothing is lost.
		msgLines := strings.Split(strings.TrimRight(l.Message, "\n"), "\n")
		fmt.Fprintf(w, "%s\t%s\t%s\n",
			l.Timestamp.Format(time.RFC3339),
			l.Level,
			msgLines[0],
		)
		for _, cont := range msgLines[1:] {
			fmt.Fprintf(w, "\t\t%s\n", cont)
		}
	}
	return w.Flush()
}

// isDaemonRunning checks if the daemon is currently running.
func isDaemonRunning() (bool, error) {
	mgr, err := daemon.NewServiceManager()
	if err != nil {
		return false, err
	}
	return mgr.IsRunning()
}

// ensureDaemonRunning starts the daemon if it's not already running.
func ensureDaemonRunning() error {
	running, err := isDaemonRunning()
	if err == nil && running {
		return nil
	}
	return runDaemonStart(false)
}
