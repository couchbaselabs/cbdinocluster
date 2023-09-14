package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func fetchClusterDef(
	simpleStr, defStr, defPath string,
) (*clusterdef.Cluster, error) {
	onlyOneDefErr := errors.New("must specify only one form of cluster definition")

	if simpleStr != "" {
		if defStr != "" || defPath != "" {
			return nil, onlyOneDefErr
		}

		shortDef, err := clusterdef.FromShortString(simpleStr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse definition short string")
		}

		return shortDef, nil
	} else if defStr != "" {
		if simpleStr != "" || defPath != "" {
			return nil, onlyOneDefErr
		}

		parsedDef, err := clusterdef.Parse([]byte(defStr))
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse cluster definition")
		}

		return parsedDef, nil
	} else if defPath != "" {
		if simpleStr != "" || defStr != "" {
			return nil, onlyOneDefErr
		}

		defFileBytes, err := os.ReadFile(defPath)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read cluster definition file")
		}

		parsedDef, err := clusterdef.Parse(defFileBytes)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse cluster definition from file")
		}

		return parsedDef, nil
	}

	return nil, errors.New("must specify at least one form of cluster definition")
}

var allocateCmd = &cobra.Command{
	Use:     "allocate [flags] [definition-tag | --def | --def-file]",
	Aliases: []string{"alloc", "create"},
	Short:   "Allocates a cluster",
	Example: "allocate simple:7.0.0\nallocate single:7.2.0",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")
		purpose, _ := cmd.Flags().GetString("purpose")
		expiry, _ := cmd.Flags().GetDuration("expiry")
		deployerName, _ := cmd.Flags().GetString("deployer")
		cloudProvider, _ := cmd.Flags().GetString("cloud-provider")

		var def *clusterdef.Cluster

		simpleDefStr := ""
		if len(args) >= 1 {
			simpleDefStr = args[0]
		}

		def, err := fetchClusterDef(simpleDefStr, defStr, defFile)
		if err != nil {
			logger.Fatal("failed to get definition", zap.Error(err))
		}

		if purpose != "" {
			def.Purpose = purpose
		}
		if expiry > 0 {
			def.Expiry = expiry
		}
		if deployerName != "" {
			def.Deployer = deployerName
		}
		if cloudProvider != "" {
			def.Cloud.CloudProvider = cloudProvider
		}

		logger.Info("deploying definition", zap.Any("def", def))

		var deployer deployment.Deployer
		if def.Deployer == "" {
			deployer = helper.GetDefaultDeployer(ctx)
		} else {
			deployer = helper.GetDeployerByName(ctx, def.Deployer)
		}

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
	allocateCmd.Flags().String("deployer", "", "The name of the deployer to use")
	allocateCmd.Flags().String("cloud-provider", "", "The cloud provider to use for this cluster")
}
