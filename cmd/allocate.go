package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var allocateCmd = &cobra.Command{
	Use:     "allocate [flags] [definition-tag | --def | --def-file]",
	Aliases: []string{"alloc", "create"},
	Short:   "Allocates a cluster",
	Example: "allocate simple:7.0.0\nallocate single:7.2.0",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")
		purpose, _ := cmd.Flags().GetString("purpose")
		expiry, _ := cmd.Flags().GetDuration("expiry")
		cloudProvider, _ := cmd.Flags().GetString("cloud-provider")

		var def *clusterdef.Cluster

		if len(args) >= 1 {
			if def != nil {
				logger.Fatal("must specify only a single tag, definition or definition file")
			}

			shortDef, err := clusterdef.FromShortString(args[0])
			if err != nil {
				logger.Fatal("failed to parse definition short string",
					zap.Error(err))
			}

			def = shortDef
		}

		if defStr != "" {
			if def != nil {
				logger.Fatal("must specify only a single tag, definition or definition file")
			}

			parsedDef, err := clusterdef.Parse([]byte(defStr))
			if err != nil {
				logger.Fatal("failed to parse cluster definition", zap.Error(err))
			}

			def = parsedDef
		}

		if defFile != "" {
			if def != nil {
				logger.Fatal("must specify only a single tag, definition or definition file")
			}

			defFileBytes, err := os.ReadFile(defFile)
			if err != nil {
				logger.Fatal("failed to read definition file '%s': %s", zap.Error(err), zap.String("file", defFile))
			}

			parsedDef, err := clusterdef.Parse(defFileBytes)
			if err != nil {
				logger.Fatal("failed to parse cluster definition from file", zap.Error(err), zap.String("file", defFile))
			}

			def = parsedDef
		}

		if def == nil {
			logger.Fatal("must specify a definition tag, definition or definition file")
		}

		if purpose != "" {
			def.Purpose = purpose
		}
		if expiry > 0 {
			def.Expiry = expiry
		}
		if cloudProvider != "" {
			if def.CloudCluster == nil {
				def.CloudCluster = &clusterdef.CloudCluster{}
			}
			def.CloudCluster.CloudProvider = cloudProvider
		}

		logger.Info("deploying definition", zap.Any("def", def))

		cluster, err := deployer.NewCluster(ctx, def)
		if err != nil {
			logger.Fatal("cluster deployment failed", zap.Error(err))
		}

		fmt.Printf("%s\n", cluster.GetID())
	},
}

func init() {
	rootCmd.AddCommand(allocateCmd)

	allocateCmd.Flags().String("def", "", "The cluster definition you wish to provision.")
	allocateCmd.Flags().String("def-file", "", "The path to a file containing a cluster definition to provision.")
	allocateCmd.Flags().String("purpose", "", "The purpose for allocating this cluster")
	allocateCmd.Flags().Duration("expiry", 1*time.Hour, "The time to keep this cluster allocated for")
	allocateCmd.Flags().String("cloud-provider", "", "The cloud provider to use for this cluster")
}
