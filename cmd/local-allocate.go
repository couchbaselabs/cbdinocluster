package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clustercontrol"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/versionident"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

type AllocateNodeDef struct {
	Count    int      `yaml:"count"`
	Name     string   `yaml:"name"`
	Version  string   `yaml:"version"`
	Services []string `json:"services"`
}

type AllocateDef struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`

	KvMemoryMB       int `yaml:"kv_memory"`
	IndexMemoryMB    int `yaml:"index_memory"`
	FtsMemoryMB      int `yaml:"fts_memory"`
	CbasMemoryMB     int `yaml:"cbas_memory"`
	EventingMemoryMB int `yaml:"eventing_memory"`

	Nodes []AllocateNodeDef `yaml:"nodes"`
}

var localAllocateCmd = &cobra.Command{
	Use:     "allocate [flags] [definition-tag | --def | --def-file]",
	Aliases: []string{"alloc", "create"},
	Short:   "Allocates a cluster",
	Example: "allocate simple:7.0.0\nallocate single:7.2.0",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)
		currentUser := helper.IdentifyCurrentUser()

		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")
		purpose, _ := cmd.Flags().GetString("purpose")
		expiry, _ := cmd.Flags().GetDuration("expiry")

		var def *AllocateDef

		if len(args) >= 1 {
			if def != nil {
				logger.Fatal("must specify only a single tag, definition or definition file")
			}

			defId := args[0]
			defIdParts := strings.Split(defId, ":")
			if len(defIdParts) != 2 {
				logger.Fatal("unexpected definition id format")
			}

			defName := defIdParts[0]
			defVersion := defIdParts[1]

			_, err := versionident.Identify(context.Background(), defVersion)
			if err != nil {
				logger.Fatal("failed to parse definition version", zap.Error(err))
			}

			if defName == "simple" {
				def = &AllocateDef{
					Nodes: []AllocateNodeDef{
						{
							Count:    3,
							Version:  defVersion,
							Services: []string{"kv", "query", "index", "fts"},
						},
					},
				}
			} else if defName == "single" {
				def = &AllocateDef{
					Nodes: []AllocateNodeDef{
						{
							Count:    1,
							Version:  defVersion,
							Services: []string{"kv", "query", "index", "fts"},
						},
					},
				}
			} else {
				logger.Fatal("unknown definition name", zap.String("name", defName))
			}
		}

		if defStr != "" {
			if def != nil {
				logger.Fatal("must specify only a single tag, definition or definition file")
			}

			var parsedDef AllocateDef
			err := yaml.Unmarshal([]byte(defStr), &parsedDef)
			if err != nil {
				logger.Fatal("failed to parse cluster definition", zap.Error(err))
			}

			def = &parsedDef
		}

		if defFile != "" {
			if def != nil {
				logger.Fatal("must specify only a single tag, definition or definition file")
			}

			defFileBytes, err := os.ReadFile(defFile)
			if err != nil {
				logger.Fatal("failed to read definition file '%s': %s", zap.Error(err), zap.String("file", defFile))
			}

			var parsedDef AllocateDef
			err = yaml.Unmarshal(defFileBytes, &parsedDef)
			if err != nil {
				logger.Fatal("failed to parse cluster definition from file", zap.Error(err), zap.String("file", defFile))
			}

			def = &parsedDef
		}

		if def == nil {
			logger.Fatal("must specify a definition tag, definition or definition file")
		}

		if expiry < 0 {
			logger.Fatal("must specify a positive expiry time")
		} else if expiry == 0 {
			expiry = 1 * time.Hour
		}

		var nodeDeployDefs []*deployment.NewClusterNodeOptions
		var nodeSetupDefs []*clustercontrol.SetupNewClusterNodeOptions
		for _, nodeDef := range def.Nodes {
			nodeCount := nodeDef.Count
			if nodeCount <= 0 {
				nodeCount = 1
			}

			if nodeDef.Version == "" {
				logger.Fatal("missing version for a node definition")
			}

			nodeServices := nodeDef.Services
			if len(nodeServices) == 0 {
				nodeServices = []string{"kv", "query", "index", "fts"}
			}

			identVersion, err := versionident.Identify(ctx, nodeDef.Version)
			if err != nil {
				logger.Fatal("failed to identify specified version\n", zap.Error(err), zap.String("version", nodeDef.Version))
			}

			for nodeDupIdx := 0; nodeDupIdx < nodeCount; nodeDupIdx++ {
				nodeName := nodeDef.Name
				if nodeName == "" {
					nodeName = fmt.Sprintf("node_%d", len(nodeDeployDefs))
				} else if nodeCount > 0 {
					nodeName = fmt.Sprintf("%s_%d", nodeName, nodeDupIdx)
				}

				nodeDeployDef := &deployment.NewClusterNodeOptions{
					Name:                nodeName,
					Version:             identVersion.Version,
					BuildNo:             identVersion.BuildNo,
					UseCommunityEdition: identVersion.CommunityEdition,
					UseServerless:       identVersion.Serverless,
				}

				nodeSetupDef := &clustercontrol.SetupNewClusterNodeOptions{
					Address: "", // this is set after provisioning

					NodeSetupOptions: clustercontrol.NodeSetupOptions{
						EnableKvService:       slices.Contains(nodeServices, "kv"),
						EnableN1qlService:     slices.Contains(nodeServices, "query"),
						EnableIndexService:    slices.Contains(nodeServices, "index"),
						EnableFtsService:      slices.Contains(nodeServices, "fts"),
						EnableCbasService:     slices.Contains(nodeServices, "cbas"),
						EnableEventingService: slices.Contains(nodeServices, "eventing"),
						EnableBackupService:   slices.Contains(nodeServices, "backup"),
					},
				}

				nodeDeployDefs = append(nodeDeployDefs, nodeDeployDef)
				nodeSetupDefs = append(nodeSetupDefs, nodeSetupDef)
			}
		}

		clusterDeployDef := &deployment.NewClusterOptions{
			Creator: currentUser,
			Purpose: purpose,
			Expiry:  expiry,
			Nodes:   nodeDeployDefs,
		}

		clusterSetupDef := &clustercontrol.SetupNewClusterOptions{
			KvMemoryQuotaMB:       def.KvMemoryMB,
			IndexMemoryQuotaMB:    def.IndexMemoryMB,
			FtsMemoryQuotaMB:      def.FtsMemoryMB,
			CbasMemoryQuotaMB:     def.CbasMemoryMB,
			EventingMemoryQuotaMB: def.EventingMemoryMB,

			Username: def.Username,
			Password: def.Password,

			Nodes: nodeSetupDefs,
		}

		if clusterSetupDef.KvMemoryQuotaMB == 0 {
			clusterSetupDef.KvMemoryQuotaMB = 256
		} else if clusterSetupDef.KvMemoryQuotaMB < 256 {
			logger.Warn("kv memory quota must be at least 256, adjusting it...")
			clusterSetupDef.KvMemoryQuotaMB = 256
		}
		if clusterSetupDef.IndexMemoryQuotaMB == 0 {
			clusterSetupDef.IndexMemoryQuotaMB = 256
		} else if clusterSetupDef.IndexMemoryQuotaMB < 256 {
			logger.Warn("index memory quota must be at least 256, adjusting it...")
			clusterSetupDef.IndexMemoryQuotaMB = 256
		}
		if clusterSetupDef.FtsMemoryQuotaMB == 0 {
			clusterSetupDef.FtsMemoryQuotaMB = 256
		} else if clusterSetupDef.FtsMemoryQuotaMB < 256 {
			logger.Warn("fts memory quota must be at least 256, adjusting it...")
			clusterSetupDef.FtsMemoryQuotaMB = 256
		}
		if clusterSetupDef.CbasMemoryQuotaMB == 0 {
			clusterSetupDef.CbasMemoryQuotaMB = 1024
		} else if clusterSetupDef.CbasMemoryQuotaMB < 1024 {
			logger.Warn("cbas memory quota must be at least 1024, adjusting it...")
			clusterSetupDef.CbasMemoryQuotaMB = 1024
		}
		if clusterSetupDef.EventingMemoryQuotaMB == 0 {
			clusterSetupDef.EventingMemoryQuotaMB = 256
		} else if clusterSetupDef.EventingMemoryQuotaMB < 256 {
			logger.Warn("eventing memory quota must be at least 256, adjusting it...")
			clusterSetupDef.EventingMemoryQuotaMB = 256
		}

		if clusterSetupDef.Username == "" {
			clusterSetupDef.Username = "Administrator"
		}
		if clusterSetupDef.Password == "" {
			clusterSetupDef.Password = "password"
		}

		logger.Info("prepared cluster definition", zap.Any("config", map[string]interface{}{
			"metaData": map[string]interface{}{
				"creator": clusterDeployDef.Creator,
				"purpose": clusterDeployDef.Purpose,
				"expiry":  clusterDeployDef.Expiry.String(),
			},
			"memoryQuotasMb": map[string]interface{}{
				"kv":       clusterSetupDef.KvMemoryQuotaMB,
				"indexer":  clusterSetupDef.IndexMemoryQuotaMB,
				"fts":      clusterSetupDef.FtsMemoryQuotaMB,
				"cbas":     clusterSetupDef.CbasMemoryQuotaMB,
				"eventing": clusterSetupDef.EventingMemoryQuotaMB,
			},
			"username": clusterSetupDef.Username,
			"password": clusterSetupDef.Password,
			"nodes": func() interface{} {
				var out []interface{}
				for nodeIdx := range nodeDeployDefs {
					deployDef := nodeDeployDefs[nodeIdx]
					setupDef := nodeSetupDefs[nodeIdx]
					out = append(out, map[string]interface{}{
						"name":    deployDef.Name,
						"version": deployDef.Version,
						"buildno": func() interface{} {
							if deployDef.BuildNo == 0 {
								return "ga"
							} else {
								return deployDef.BuildNo
							}
						}(),
						"edition": func() interface{} {
							if deployDef.UseCommunityEdition {
								return "community"
							} else {
								return "enterprise"
							}
						}(),
						"serverless": func() interface{} {
							if deployDef.UseServerless {
								return "yes"
							} else {
								return "no"
							}
						}(),
						"services": setupDef.ServicesList(),
					})
				}
				return out
			}(),
		}))

		cluster, err := deployer.NewCluster(ctx, clusterDeployDef)
		if err != nil {
			logger.Fatal("cluster deployment failed", zap.Error(err))
		}

		for nodeIdx, node := range cluster.Nodes {
			nodeSetupDefs[nodeIdx].Address = node.IPAddress
		}

		clusterMgr := clustercontrol.ClusterManager{
			Logger: logger,
		}
		err = clusterMgr.SetupNewCluster(ctx, clusterSetupDef)
		if err != nil {
			logger.Fatal("cluster setup failed", zap.Error(err))
		}

		fmt.Printf("%s\n", cluster.ClusterID)
	},
}

func init() {
	localCmd.AddCommand(localAllocateCmd)

	localAllocateCmd.Flags().String("def", "", "The cluster definition you wish to provision.")
	localAllocateCmd.Flags().String("def-file", "", "The path to a file containing a cluster definition to provision.")
	localAllocateCmd.Flags().String("purpose", "", "The purpose for allocating this cluster")
	localAllocateCmd.Flags().Duration("expiry", 1*time.Hour, "The time to keep this cluster allocated for")
}
