// Package cli provides the root Cobra command and wires all subcommands.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd is the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "gitgate",
	Short: "A local quality gate before pushing to remote Git",
	Long: `GitGate acts as an intermediary Git remote that runs a configurable
pipeline (rebase, AI review, tests, lint) before forwarding your push to
the real remote and optionally opening a Pull Request.

Example:
  git push gitgate main    # Push through the quality gate
  git push origin main     # Bypass the gate (direct push)
`,
	SilenceUsage: true,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: .gitgate.yml or ~/.gitgate/config.yml)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().Bool("json", false, "Output results as JSON (for automation/agent use)")

	// Register subcommands
	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newDaemonCmd())
	rootCmd.AddCommand(newDoctorCmd())
	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newRespondCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newUpdateCmd())
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Look for .gitgate.yml in current directory first
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.gitgate")
		viper.SetConfigName(".gitgate")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("GITGATE")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "Warning: error reading config file: %v\n", err)
		}
	}
}
