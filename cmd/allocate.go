package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var allocateCmd = &cobra.Command{
	Use:     "allocate [flags] <definition-tag | --def | --def-file>",
	Aliases: []string{"alloc", "create"},
	Short:   "Allocates a cluster",
	Example: "allocate simple:7.0.0\nallocate single:7.2.0",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		dryRun, _ := cmd.Flags().GetBool("dry-run")
		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")
		purpose, _ := cmd.Flags().GetString("purpose")
		expiry, _ := cmd.Flags().GetDuration("expiry")
		expiryIsSet := cmd.Flags().Changed("expiry")
		deployerName, _ := cmd.Flags().GetString("deployer")
		cloudProvider, _ := cmd.Flags().GetString("cloud-provider")

		var def *clusterdef.Cluster

		simpleDefStr := ""
		if len(args) >= 1 {
			simpleDefStr = args[0]
		}

		def, err := helper.FetchClusterDef(simpleDefStr, defStr, defFile)
		if err != nil {
			logger.Fatal("failed to get definition", zap.Error(err))
		}

		if purpose != "" {
			def.Purpose = purpose
		}
		if expiryIsSet {
			def.Expiry = expiry
		} else if def.Expiry == 0 {
			def.Expiry = config.DefaultExpiry
		}
		if deployerName != "" {
			def.Deployer = deployerName
		}
		if cloudProvider != "" {
			def.Cloud.CloudProvider = cloudProvider
		}

		logger.Info("deploying definition", zap.Any("def", def))

		if dryRun {
			return
		}

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

		if len(def.Buckets) > 0 {
			for bucketName, bucketDef := range def.Buckets {
				err := deployer.CreateBucket(ctx, cluster.GetID(), &deployment.CreateBucketOptions{
					Name:         bucketName,
					RamQuotaMB:   bucketDef.Settings.RamQuotaMB,
					FlushEnabled: bucketDef.Settings.FlushEnabled,
					NumReplicas:  bucketDef.Settings.NumReplicas,
				})
				if err != nil {
					logger.Fatal("failed to create bucket", zap.String("bucket", bucketName), zap.Error(err))
				}
				logger.Info("bucket created", zap.String("bucket", bucketName))

				for scopeName, collections := range bucketDef.Scopes {
					if scopeName == "" {
						continue
					}
					if err := deployer.CreateScope(ctx, cluster.GetID(), bucketName, scopeName); err != nil {
						logger.Fatal("failed to create scope", zap.String("bucket", bucketName), zap.String("scope", scopeName), zap.Error(err))
					}
					logger.Info("scope created", zap.String("bucket", bucketName), zap.String("scope", scopeName))

					for _, collName := range collections {
						if collName == "" {
							continue
						}
						if err := deployer.CreateCollection(ctx, cluster.GetID(), bucketName, scopeName, collName); err != nil {
							logger.Fatal("failed to create collection", zap.String("bucket", bucketName), zap.String("scope", scopeName), zap.String("collection", collName), zap.Error(err))
						}
						logger.Info("collection created", zap.String("bucket", bucketName), zap.String("scope", scopeName), zap.String("collection", collName))
					}
				}
			}
		}

		switch cluster := cluster.(type) {
		case *clouddeploy.ClusterInfo:
			if cluster.CloudClusterID != "" {
				logger.Info("cloud cluster was allocated",
					zap.String("cloud-id", cluster.CloudClusterID))
			} else {
				logger.Warn("cloud cluster id is unavailable, deployment may have failed")
			}
		}

		// for humans using dino-cluster, we print some helpful info if available
		connectInfo, _ := deployer.GetConnectInfo(ctx, cluster.GetID())
		if connectInfo != nil {
			logger.Info("cluster deployed",
				zap.String("mgmt", connectInfo.Mgmt),
				zap.String("connstr", connectInfo.ConnStr))
		}

		fmt.Printf("%s\n", cluster.GetID())
	},
}

func init() {
	rootCmd.AddCommand(allocateCmd)

	allocateCmd.Flags().Bool("dry-run", false, "Disables the actual allocate and simply does a dry-run.")
	allocateCmd.Flags().String("def", "", "The cluster definition you wish to provision.")
	allocateCmd.Flags().String("def-file", "", "The path to a file containing a cluster definition to provision.")
	allocateCmd.Flags().String("purpose", "", "The purpose for allocating this cluster")
	allocateCmd.Flags().Duration("expiry", 0, "The time to keep this cluster allocated for")
	allocateCmd.Flags().String("deployer", "", "The name of the deployer to use")
	allocateCmd.Flags().String("cloud-provider", "", "The cloud provider to use for this cluster")
}
