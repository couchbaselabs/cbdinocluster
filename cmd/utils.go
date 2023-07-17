package cmd

import (
	"context"
	"log"
	"runtime"
	"strings"

	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/brett19/cbdyncluster2/deployment/dockerdeploy"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
)

func getDeployer(ctx context.Context) deployment.Deployer {
	dockerCli, err := client.NewClientWithOpts(
		client.WithHostFromEnv(),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		log.Fatalf("docker connect error: %s", err)
	}

	_, err = dockerCli.Ping(ctx)
	if err != nil {
		log.Fatalf("failed to ping docker host: %s", err)
	}

	networks, err := dockerCli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		log.Fatalf("failed to list docker networks: %s", err)
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
			log.Fatalf("must use macvlan0 network on windows and mac but none was found")
		}

		selectedNetwork = ""
	}

	deployer, err := dockerdeploy.NewDeployer(&dockerdeploy.DeployerOptions{
		DockerCli:    dockerCli,
		NetworkName:  selectedNetwork,
		GhcrUsername: "",
		GhcrPassword: "",
	})
	if err != nil {
		log.Fatalf("deployer init failed: %s", err)
	}

	err = deployer.Cleanup(ctx)
	if err != nil {
		log.Fatalf("failed to execute cleanup: %s", err)
	}

	return deployer
}

func identifyCluster(ctx context.Context, deployer deployment.Deployer, userInput string) (*deployment.ClusterInfo, error) {
	clusters, err := deployer.ListClusters(ctx)
	if err != nil {
		log.Fatalf("failed to list clusters: %s", err)
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
