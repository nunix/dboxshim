package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dboxshim",
	Short: "DboxShim is a modern TUI for managing Distrobox instances",
	Long:  `DboxShim wraps distrobox commands providing a nice, fast TUI experience.`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
