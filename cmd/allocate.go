package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/brett19/cbdyncluster2/clustercontrol"
	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/brett19/cbdyncluster2/versionident"
	"github.com/spf13/cobra"
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

var allocateCmd = &cobra.Command{
	Use:     "allocate [flags] [definition-tag | --def | --def-file]",
	Aliases: []string{"create", "alloc"},
	Short:   "Allocates a cluster",
	Example: "allocate simple:7.0.0\nallocate single:7.2.0",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")
		purpose, _ := cmd.Flags().GetString("purpose")
		expiry, _ := cmd.Flags().GetDuration("expiry")

		currentUser := identifyCurrentUser()

		var def *AllocateDef

		if len(args) >= 1 {
			if def != nil {
				log.Fatalf("must specify only a single tag, definition or definition file")
			}

			defId := args[0]
			defIdParts := strings.Split(defId, ":")
			if len(defIdParts) != 2 {
				log.Fatalf("unexpected definition id format")
			}

			defName := defIdParts[0]
			defVersion := defIdParts[1]

			_, err := versionident.Identify(context.Background(), defVersion)
			if err != nil {
				log.Fatalf("failed to parse definition version: %s", err)
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
				log.Fatalf("unknown definition name: %s", defName)
			}
		}

		if defStr != "" {
			if def != nil {
				log.Fatalf("must specify only a single tag, definition or definition file")
			}

			var parsedDef AllocateDef
			err := yaml.Unmarshal([]byte(defStr), &parsedDef)
			if err != nil {
				log.Fatalf("failed to parse cluster definition: %s", err)
			}

			def = &parsedDef
		}

		if defFile != "" {
			if def != nil {
				log.Fatalf("must specify only a single tag, definition or definition file")
			}

			defFileBytes, err := os.ReadFile(defFile)
			if err != nil {
				log.Fatalf("failed to read definition file '%s': %s", defFile, err)
			}

			var parsedDef AllocateDef
			err = yaml.Unmarshal(defFileBytes, &parsedDef)
			if err != nil {
				log.Fatalf("failed to parse cluster definition from file '%s': %s", defFile, err)
			}

			def = &parsedDef
		}

		if def == nil {
			log.Fatalf("must specify a definition tag, definition or definition file")
		}

		if expiry < 0 {
			log.Fatalf("must specify a positive expiry time")
		} else if expiry == 0 {
			expiry = 1 * time.Hour
		}

		deployer := getDeployer(ctx)

		var nodeDeployDefs []*deployment.NewClusterNodeOptions
		var nodeSetupDefs []*clustercontrol.SetupNewClusterNodeOptions
		for _, nodeDef := range def.Nodes {
			nodeCount := nodeDef.Count
			if nodeCount <= 0 {
				nodeCount = 1
			}

			if nodeDef.Version == "" {
				log.Fatalf("missing version for a node definition")
			}

			nodeServices := nodeDef.Services
			if len(nodeServices) == 0 {
				nodeServices = []string{"kv", "query", "index", "fts"}
			}

			identVersion, err := versionident.Identify(ctx, nodeDef.Version)
			if err != nil {
				log.Fatalf("failed to identify specified version '%s': %s\n", nodeDef.Version, err)
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
			log.Printf("kv memory quota must be at least 256, adjusting it...")
			clusterSetupDef.KvMemoryQuotaMB = 256
		}
		if clusterSetupDef.IndexMemoryQuotaMB == 0 {
			clusterSetupDef.IndexMemoryQuotaMB = 256
		} else if clusterSetupDef.IndexMemoryQuotaMB < 256 {
			log.Printf("index memory quota must be at least 256, adjusting it...")
			clusterSetupDef.IndexMemoryQuotaMB = 256
		}
		if clusterSetupDef.FtsMemoryQuotaMB == 0 {
			clusterSetupDef.FtsMemoryQuotaMB = 256
		} else if clusterSetupDef.FtsMemoryQuotaMB < 256 {
			log.Printf("fts memory quota must be at least 256, adjusting it...")
			clusterSetupDef.FtsMemoryQuotaMB = 256
		}
		if clusterSetupDef.CbasMemoryQuotaMB == 0 {
			clusterSetupDef.CbasMemoryQuotaMB = 1024
		} else if clusterSetupDef.CbasMemoryQuotaMB < 1024 {
			log.Printf("cbas memory quota must be at least 1024, adjusting it...")
			clusterSetupDef.CbasMemoryQuotaMB = 1024
		}
		if clusterSetupDef.EventingMemoryQuotaMB == 0 {
			clusterSetupDef.EventingMemoryQuotaMB = 256
		} else if clusterSetupDef.EventingMemoryQuotaMB < 256 {
			log.Printf("eventing memory quota must be at least 256, adjusting it...")
			clusterSetupDef.EventingMemoryQuotaMB = 256
		}

		if clusterSetupDef.Username == "" {
			clusterSetupDef.Username = "Administrator"
		}
		if clusterSetupDef.Password == "" {
			clusterSetupDef.Password = "password"
		}

		log.Printf("Deploying cluster with configuration:")
		log.Printf("  Meta-Data:")
		log.Printf("    Creator: %s", clusterDeployDef.Creator)
		log.Printf("    Purpose: %s", clusterDeployDef.Purpose)
		log.Printf("    Expiry: %s", clusterDeployDef.Expiry)
		log.Printf("  Memory Quotas")
		log.Printf("    Kv: %d MB", clusterSetupDef.KvMemoryQuotaMB)
		log.Printf("    Indexer: %d MB", clusterSetupDef.IndexMemoryQuotaMB)
		log.Printf("    Fts: %d MB", clusterSetupDef.FtsMemoryQuotaMB)
		log.Printf("    Cbas: %d MB", clusterSetupDef.CbasMemoryQuotaMB)
		log.Printf("    Eventing: %d MB", clusterSetupDef.EventingMemoryQuotaMB)
		log.Printf("  Username: %s", clusterSetupDef.Username)
		log.Printf("  Password: %s", clusterSetupDef.Password)
		log.Printf("  Nodes:")
		for nodeIdx := range nodeDeployDefs {
			deployDef := nodeDeployDefs[nodeIdx]
			setupDef := nodeSetupDefs[nodeIdx]
			log.Printf("    -  Name: %s", deployDef.Name)
			log.Printf("       Version: %s", deployDef.Version)
			if deployDef.BuildNo == 0 {
				log.Printf("       BuildNo: Public GA")
			} else {
				log.Printf("       BuildNo: %d", deployDef.BuildNo)
			}
			if deployDef.UseCommunityEdition {
				log.Printf("       Edition: Community Edition")
			} else {
				log.Printf("       Edition: Enterprise Edition")
			}
			if deployDef.UseServerless {
				log.Printf("       Serverless: Yes")
			} else {
				log.Printf("       Serverless: No")
			}
			log.Printf("       Services: %s", strings.Join(setupDef.ServicesList(), ", "))
		}

		cluster, err := deployer.NewCluster(ctx, clusterDeployDef)
		if err != nil {
			log.Fatalf("cluster deployment failed: %s", err)
		}

		for nodeIdx, node := range cluster.Nodes {
			nodeSetupDefs[nodeIdx].Address = node.IPAddress
		}

		clusterMgr := clustercontrol.ClusterManager{}
		err = clusterMgr.SetupNewCluster(ctx, clusterSetupDef)
		if err != nil {
			log.Fatalf("cluster setup failed: %s", err)
		}

		fmt.Printf("%s\n", cluster.ClusterID)
	},
}

func init() {
	rootCmd.AddCommand(allocateCmd)

	allocateCmd.Flags().String("def", "", "The cluster definition you wish to provision.")
	allocateCmd.Flags().String("def-file", "", "The path to a file containing a cluster definition to provision.")
	allocateCmd.Flags().String("purpose", "", "The purpose for allocating this cluster")
	allocateCmd.Flags().Duration("expiry", 1*time.Hour, "The time to keep this cluster allocated for")
}
