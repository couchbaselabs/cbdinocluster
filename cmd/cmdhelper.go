package cmd

import (
	"context"
	"log"
	"os/user"
	"runtime"
	"strings"

	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/brett19/cbdyncluster2/deployment/dockerdeploy"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type CmdHelper struct {
	logger *zap.Logger
}

func (h *CmdHelper) GetContext() context.Context {
	return context.Background()
}

func (h *CmdHelper) GetLogger() *zap.Logger {
	if h.logger == nil {
		verbose, _ := rootCmd.Flags().GetBool("verbose")

		logConfig := zap.NewDevelopmentConfig()
		if !verbose {
			logConfig.Level.SetLevel(zap.InfoLevel)
			logConfig.DisableCaller = true
		}

		logger, err := logConfig.Build()
		if err != nil {
			log.Fatalf("failed to initialize verbose logger: %s", err)
		}

		h.logger = logger
	}

	return h.logger
}

func (h *CmdHelper) GetDeployer(ctx context.Context) deployment.Deployer {
	logger := h.GetLogger()

	dockerCli, err := client.NewClientWithOpts(
		client.WithHostFromEnv(),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		logger.Fatal("failed to connect to docker", zap.Error(err))
	}

	_, err = dockerCli.Ping(ctx)
	if err != nil {
		logger.Fatal("failed to ping docker", zap.Error(err))
	}

	networks, err := dockerCli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		logger.Fatal("failed to list docker networks", zap.Error(err))
	}

	foundMacVlanNetwork := false
	for _, network := range networks {
		if network.Name == "macvlan0" {
			foundMacVlanNetwork = true
		}
	}

	var selectedNetwork string
	if foundMacVlanNetwork {
		selectedNetwork = "macvlan0"
	} else {
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			logger.Fatal("must use macvlan0 network on windows and mac but none was found")
		}

		selectedNetwork = ""
	}

	deployer, err := dockerdeploy.NewDeployer(&dockerdeploy.DeployerOptions{
		Logger:       logger,
		DockerCli:    dockerCli,
		NetworkName:  selectedNetwork,
		GhcrUsername: "",
		GhcrPassword: "",
	})
	if err != nil {
		logger.Fatal("failed to initialize deployer", zap.Error(err))
	}

	err = deployer.Cleanup(ctx)
	if err != nil {
		logger.Fatal("failed to run pre-cleanup", zap.Error(err))
	}

	return deployer
}

func (h *CmdHelper) IdentifyCurrentUser() string {
	osUser, err := user.Current()
	if err != nil {
		return ""
	}

	return osUser.Username
}

func (h *CmdHelper) IdentifyCluster(ctx context.Context, deployer deployment.Deployer, userInput string) (*deployment.ClusterInfo, error) {
	clusters, err := deployer.ListClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}

	var identifiedCluster *deployment.ClusterInfo

	for _, cluster := range clusters {
		if strings.HasPrefix(cluster.ClusterID, userInput) {
			if identifiedCluster != nil {
				return nil, errors.New("multiple clusters matched the specified identifier")
			}

			identifiedCluster = cluster
		}
	}

	if identifiedCluster == nil {
		return nil, errors.New("no clusters matched the specified identifier")
	}

	return identifiedCluster, nil
}

func (h *CmdHelper) IdentifyNode(ctx context.Context, cluster *deployment.ClusterInfo, userInput string) (*deployment.ClusterNodeInfo, error) {
	var identifiedNode *deployment.ClusterNodeInfo

	for _, node := range cluster.Nodes {
		if strings.HasPrefix(node.NodeID, userInput) || node.Name == userInput {
			if identifiedNode != nil {
				return nil, errors.New("multiple nodes matched the specified identifier")
			}

			identifiedNode = node
		}
	}

	if identifiedNode == nil {
		return nil, errors.New("no nodes matched the specified identifier")
	}

	return identifiedNode, nil
}
