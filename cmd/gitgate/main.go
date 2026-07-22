package main

import (
	"os"

	"github.com/jvrsantacruz/gitgate/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
