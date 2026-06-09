package cmd

import (
	"errors"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var bucketsAddCmd = &cobra.Command{
	Use:   "add <cluster-id> <bucket-name>",
	Short: "Adds a new bucket",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]

		ramQuotaMB, _ := cmd.Flags().GetInt("ram-quota-mb")
		flushEnabled, _ := cmd.Flags().GetBool("flush-enabled")
		numReplicas, _ := cmd.Flags().GetInt("num-replicas")
		bucketTypeStr, _ := cmd.Flags().GetString("bucket-type")

		createOpts, err := newCreateBucketOptions(bucketName, bucketTypeStr, ramQuotaMB, flushEnabled, numReplicas)
		if err != nil {
			logger.Fatal("invalid bucket type", zap.Error(err))
		}

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err = deployer.CreateBucket(ctx, cluster.GetID(), createOpts)
		if err != nil {
			if errors.Is(err, deployment.ErrBucketAlreadyExists) {
				logger.Fatal("failed to create bucket as it already exists")
			}
			logger.Fatal("failed to create bucket", zap.Error(err))
		}
	},
}

// newCreateBucketOptions parses the user-supplied bucket type and assembles the
// CreateBucketOptions shared by the `buckets add` command and the YAML-driven
// allocate path. It returns an error for an unrecognized bucket type.
func newCreateBucketOptions(name, bucketTypeStr string, ramQuotaMB int, flushEnabled bool, numReplicas int) (*deployment.CreateBucketOptions, error) {
	bucketType, err := deployment.ParseBucketType(bucketTypeStr)
	if err != nil {
		return nil, err
	}

	return &deployment.CreateBucketOptions{
		Name:         name,
		BucketType:   bucketType,
		RamQuotaMB:   ramQuotaMB,
		FlushEnabled: flushEnabled,
		NumReplicas:  numReplicas,
	}, nil
}

func init() {
	bucketsCmd.AddCommand(bucketsAddCmd)

	bucketsAddCmd.Flags().String("bucket-type", "couchbase", "The type of bucket to create: couchbase (default) or ephemeral. memcached is a legacy type that Couchbase Server 8.0+ rejects and is only creatable on older clusters.")
	bucketsAddCmd.Flags().Int("ram-quota-mb", 0, "The amount of RAM to provide for the bucket.")
	bucketsAddCmd.Flags().Bool("flush-enabled", false, "Whether flush is enabled on the bucket.")
	bucketsAddCmd.Flags().Int("num-replicas", 1, "The number of replicas for the bucket.")
}
