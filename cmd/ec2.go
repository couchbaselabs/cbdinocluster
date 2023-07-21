package cmd

import (
	"github.com/spf13/cobra"
)

var ec2Cmd = &cobra.Command{
	Use:   "ec2",
	Short: "Provides access to tools related to EC2",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(ec2Cmd)
}
