package cmd

import (
	"context"
	"log"
	"os"
	"os/user"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/brett19/cbdyncluster2/capellacontrol"
	"github.com/brett19/cbdyncluster2/cbdcconfig"
	"github.com/brett19/cbdyncluster2/cloudprovision"
	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/brett19/cbdyncluster2/deployment/dockerdeploy"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type CmdHelper struct {
	logger *zap.Logger

	config *cbdcconfig.Config
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

		logger.Info("logger initialized")

		h.logger = logger
	}

	return h.logger
}

func (h *CmdHelper) GetConfig(ctx context.Context) *cbdcconfig.Config {
	logger := h.GetLogger()

	if h.config == nil {
		curConfig, err := cbdcconfig.Load(ctx)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				logger.Fatal("failed to load config file", zap.Error(err))
			}
		}

		if curConfig == nil ||
			curConfig.Docker == nil ||
			curConfig.GitHub == nil ||
			curConfig.AWS == nil ||
			curConfig.Capella == nil {
			logger.Fatal("you must run the `init` command first")
		}

		h.config = curConfig
	}

	return h.config
}

func (h *CmdHelper) GetDeployer(ctx context.Context) deployment.Deployer {
	logger := h.GetLogger()
	config := h.GetConfig(ctx)

	githubToken := config.GitHub.Token
	githubUser := config.GitHub.User
	dockerHost := config.Docker.Host
	dockerNetwork := config.Docker.Network

	dockerCli, err := client.NewClientWithOpts(
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		logger.Fatal("failed to connect to docker", zap.Error(err))
	}

	deployer, err := dockerdeploy.NewDeployer(&dockerdeploy.DeployerOptions{
		Logger:       logger,
		DockerCli:    dockerCli,
		NetworkName:  dockerNetwork,
		GhcrUsername: githubUser,
		GhcrPassword: githubToken,
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

func (h *CmdHelper) GetCloudProvisioner(ctx context.Context) *cloudprovision.Provisioner {
	logger := h.GetLogger()
	config := h.GetConfig(ctx)

	capellaUser := config.Capella.Username
	capellaPass := config.Capella.Password
	capellaOid := config.Capella.OrganizationID

	client, err := capellacontrol.NewController(ctx, &capellacontrol.ControllerOptions{
		Logger:   logger,
		Endpoint: "https://api.cloud.couchbase.com",
		Auth: &capellacontrol.BasicCredentials{
			Username: capellaUser,
			Password: capellaPass,
		},
	})
	if err != nil {
		logger.Fatal("failed to create controller", zap.Error(err))
	}

	prov, err := cloudprovision.NewProvisioner(&cloudprovision.NewProvisionerOptions{
		Logger:   logger,
		Client:   client,
		TenantID: capellaOid,
	})
	if err != nil {
		logger.Fatal("failed to create provisioner", zap.Error(err))
	}

	// This can take a long time sometimes, so this is only run manually.
	/*
		err = prov.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to run pre-cleanup", zap.Error(err))
		}
	*/

	return prov
}

func (h *CmdHelper) GetAWSCredentials(ctx context.Context) aws.Credentials {
	logger := h.GetLogger()
	cbdcConfig := h.GetConfig(ctx)

	if cbdcConfig.AWS.FromEnvironment {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			logger.Fatal("failed to load AWS config", zap.Error(err))
		}

		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			logger.Fatal("failed to retreive AWS credentials", zap.Error(err))
		}

		return creds
	} else {
		if cbdcConfig.AWS.AccessKey == "" || cbdcConfig.AWS.SecretKey == "" {
			logger.Fatal("cannot use AWS without credentials")
		}

		return aws.Credentials{
			AccessKeyID:     cbdcConfig.AWS.AccessKey,
			SecretAccessKey: cbdcConfig.AWS.SecretKey,
		}
	}
}

func (h *CmdHelper) IdentifyCurrentUser() string {
	osUser, err := user.Current()
	if err != nil {
		return ""
	}

	return osUser.Username
}

func (h *CmdHelper) IdentifyCluster(ctx context.Context, deployer deployment.Deployer, userInput string) (*deployment.ClusterInfo, error) {
	h.logger.Info("attempting to identify cluster", zap.String("input", userInput))
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
	h.logger.Info("attempting to identify cluster node", zap.String("input", userInput))

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

func (h *CmdHelper) IdentifyCloudCluster(ctx context.Context, prov *cloudprovision.Provisioner, userInput string) (*cloudprovision.ClusterInfo, error) {
	h.logger.Info("attempting to identify cloud cluster", zap.String("input", userInput))

	clusters, err := prov.ListClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}

	var identifiedCluster *cloudprovision.ClusterInfo

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
