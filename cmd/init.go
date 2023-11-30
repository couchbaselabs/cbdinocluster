package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cloudinstancecontrol"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/go-github/v53/github"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
)

var initCmd = &cobra.Command{
	Use:   "init [flags]",
	Short: "Initializes the tool",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		autoConfig, _ := cmd.Flags().GetBool("auto")

		userHomePath, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("Failed to identify user home directory (%s)\n", err)
		}

		curConfig, _ := cbdcconfig.Load(ctx)
		if curConfig == nil {
			curConfig = &cbdcconfig.Config{
				Version: cbdcconfig.Version,
			}
		}

		readString := func(q string, defaultValue string, sensitive bool) string {
			printableValue := defaultValue
			if sensitive {
				printableValue = strings.Repeat("*", len(defaultValue))
			}
			fmt.Printf("%s [%s]: ", q, printableValue)

			if autoConfig {
				fmt.Printf("\n")
				return defaultValue
			}

			var str string
			fmt.Scanf("%s", &str)
			str = strings.TrimSpace(str)
			if str == "" {
				return defaultValue
			}
			return str
		}

		readBool := func(q string, defaultValue bool) bool {
			for {
				if defaultValue {
					fmt.Printf("%s [Y/n]: ", q)
				} else {
					fmt.Printf("%s [y/N]: ", q)
				}

				if autoConfig {
					fmt.Printf("\n")
					return defaultValue
				}

				var str string
				fmt.Scanf("%s", &str)
				str = strings.TrimSpace(str)
				boolStr := strings.ToLower(str)
				if boolStr == "" {
					return defaultValue
				} else if boolStr == "y" || boolStr == "yes" {
					return true
				} else if boolStr == "n" || boolStr == "no" {
					return false
				}

				fmt.Printf("Invalid entry, try again...\n")
			}
		}

		checkFileExists := func(path string) bool {
			stat, err := os.Stat(path)
			if err != nil {
				return false
			}

			if stat.IsDir() {
				return false
			}

			return true
		}

		saveConfig := func() {
			err := cbdcconfig.Save(ctx, curConfig)
			if err != nil {
				fmt.Printf("failed to write updated config: %s\n", err)
				os.Exit(1)
			}
		}

		getColimaDockerHost := func() string {
			colimaSocketPath := path.Join(userHomePath, "/.colima/default/docker.sock")
			fmt.Printf("Checking for Colima installation at `%s`... ", colimaSocketPath)
			hasColima := checkFileExists(colimaSocketPath)
			if !hasColima {
				fmt.Printf("not found.\n")
				return ""
			}

			fmt.Printf("found.\n")
			return "unix://" + colimaSocketPath
		}

		getDockerDockerHost := func() string {
			dockerSocketPath := "/var/run/docker.sock"
			fmt.Printf("Checking for Docker installation at `%s`... ", dockerSocketPath)
			hasDocker := checkFileExists(dockerSocketPath)
			if !hasDocker {
				fmt.Printf("not found.\n")
				return ""
			}

			fmt.Printf("found.\n")
			return "unix://" + dockerSocketPath
		}

		getGitHubUser := func(token string) string {
			ts := oauth2.StaticTokenSource(
				&oauth2.Token{AccessToken: token},
			)
			tc := oauth2.NewClient(ctx, ts)

			githubClient := github.NewClient(tc)
			user, _, err := githubClient.Users.Get(ctx, "")
			if err != nil {
				fmt.Printf("Failed to fetch user details with provided token:\n  %s\n", err)
				return ""
			}

			fmt.Printf("Found user details using token: %s\n", *user.Login)
			return *user.Login
		}

		identifySelf := func() (interface{}, error) {
			logger, _ := zap.NewDevelopment()
			siCtrl := cloudinstancecontrol.SelfIdentifyController{
				Logger: logger,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			ident, err := siCtrl.Identify(ctx)
			cancel()

			return ident, err
		}

		fmt.Printf("Attempting to self-identify as cloud instance\n")
		cloudIdent, err := identifySelf()
		if err != nil {
			fmt.Printf("Failed... %s\n", err)
		} else {
			fmt.Printf("Success! %+v\n", cloudIdent)
		}

		awsCloudIdent, _ := cloudIdent.(*awscontrol.LocalInstanceInfo)
		azureCloudIdent, _ := cloudIdent.(*azurecontrol.LocalVmInfo)

		printGitHubConfig := func() {
			fmt.Printf("  Enabled: %t\n", curConfig.GitHub.Enabled.Value())
			fmt.Printf("  Token: %s\n", strings.Repeat("*", len(curConfig.GitHub.Token)))
			fmt.Printf("  User: %s\n", curConfig.GitHub.User)
		}
		{
			fmt.Printf("-- GitHub Configuration\n")
			fmt.Printf("This is used to access non-public server builds on the GitHub Package Registry,\n")
			fmt.Printf("the personal access token must allow the 'read:packages' scope.\n")

			flagDisableGithub, _ := cmd.Flags().GetBool("disable-github")
			flagGithubToken, _ := cmd.Flags().GetString("github-token")
			flagGithubUser, _ := cmd.Flags().GetString("github-user")
			envGithubToken := os.Getenv("GITHUB_TOKEN")
			envGithubActor := os.Getenv("GITHUB_ACTOR")

			githubEnabled := true
			githubToken := ""
			githubUser := ""

			for {
				if flagDisableGithub {
					fmt.Printf("GitHub disabled via flags.\n")
					githubEnabled = false
					break
				}

				githubEnabled = readBool(
					"Would you like to configure Github?",
					githubEnabled)
				if !githubEnabled {
					break
				}

				if flagGithubToken != "" {
					fmt.Printf("GitHub token specified via flags:\n")
					githubToken = flagGithubToken
				} else {
					githubToken = curConfig.GitHub.Token
					if githubToken == "" && envGithubToken != "" {
						fmt.Printf("Defaulting to GitHub token from environment.\n")
						githubToken = envGithubToken
					}

					githubToken = readString(
						"What GitHub token should we use?",
						githubToken, true)
				}
				if githubToken == "" {
					fmt.Printf("A GitHub token is required.\n")
					githubEnabled = false
					continue
				}

				if flagGithubUser != "" {
					fmt.Printf("GitHub user specified via flags: %s\n", githubUser)
					githubUser = flagGithubUser
				} else {
					fmt.Printf("Fetching GitHub user using token...\n")
					fetchedUser := getGitHubUser(githubToken)

					githubUser = curConfig.GitHub.User
					if githubUser == "" && envGithubActor != "" {
						fmt.Printf("Defaulting to GitHub user from environment.\n")
						githubUser = envGithubActor
					}
					if githubUser == "" && fetchedUser != "" {
						fmt.Printf("Defaulting to GitHub user fetched from API.\n")
						githubUser = fetchedUser
					}

					githubUser = readString(
						"What GitHub user should we use?",
						githubUser, false)
				}
				if githubUser == "" {
					fmt.Printf("The GitHub user name is required.\n")
					githubEnabled = false
					continue
				}

				break
			}

			curConfig.GitHub.Enabled.Set(githubEnabled)
			curConfig.GitHub.Token = githubToken
			curConfig.GitHub.User = githubUser
			saveConfig()
		}

		printDockerConfig := func() {
			fmt.Printf("  Enabled: %t\n", curConfig.Docker.Enabled.Value())
			fmt.Printf("  Host: %s\n", curConfig.Docker.Host)
			fmt.Printf("  Network: %s\n", curConfig.Docker.Network)
			fmt.Printf("  Forward Only: %t\n", curConfig.Docker.ForwardOnly.Value())
		}
		{
			fmt.Printf("-- Docker Configuration\n")

			flagDisableDocker, _ := cmd.Flags().GetBool("disable-docker")
			flagDockerHost, _ := cmd.Flags().GetString("docker-host")
			flagDockerNetwork, _ := cmd.Flags().GetString("docker-network")
			envDockerHost := os.Getenv("DOCKER_HOST")

			dockerEnabled := true
			dockerHost := curConfig.Docker.Host
			dockerNetwork := curConfig.Docker.Network

			for {
				if flagDisableDocker {
					fmt.Printf("Docker disabled via flags.\n")
					dockerEnabled = false
					break
				}

				dockerEnabled = readBool(
					"Would you like to configure Docker Deployments?",
					dockerEnabled)
				if !dockerEnabled {
					break
				}

				if flagDockerHost != "" {
					fmt.Printf("Docker host specified via flags:\n  %s\n", flagDockerHost)
					dockerHost = flagDockerHost
				} else {
					colimaDockerHost := getColimaDockerHost()
					dockerDockerHost := getDockerDockerHost()

					if dockerHost == "" {
						fmt.Printf("Defaulting to docker host from environment.\n")
						dockerHost = envDockerHost
					}
					if dockerHost == "" && colimaDockerHost != "" {
						fmt.Printf("Defaulting to docker host from detected colima.\n")
						dockerHost = colimaDockerHost
					}
					if dockerHost == "" && dockerDockerHost != "" {
						fmt.Printf("Defaulting to docker host from detected docker.\n")
						dockerHost = dockerDockerHost
					}

					dockerHost = readString(
						"What docker host should we use?",
						dockerHost, false)
				}
				if dockerHost == "" {
					fmt.Printf("A docker host to use is required...\n")
					dockerEnabled = false
					continue
				}

				fmt.Printf("Pinging the docker host to confirm it works...\n")
				dockerCli, err := client.NewClientWithOpts(
					client.WithHost(dockerHost),
					client.WithAPIVersionNegotiation(),
				)
				if err != nil {
					fmt.Printf("Failed to setup docker client:\n  %s\n", err)
					dockerEnabled = false
					continue
				}

				_, err = dockerCli.Ping(ctx)
				if err != nil {
					fmt.Printf("Failed to ping docker:\n  %s\n", err)
					dockerEnabled = false
					continue
				}

				fmt.Printf("Success!\n")

				if flagDockerNetwork != "" {
					fmt.Printf("Docker network specified via flags:\n  %s\n", flagDockerNetwork)
					dockerNetwork = flagDockerNetwork
				} else {
					fmt.Printf("Listing docker networks:\n")
					networks, _ := dockerCli.NetworkList(ctx, types.NetworkListOptions{})
					for _, network := range networks {
						fmt.Printf("  %s\n", network.Name)
					}

					hasMacVlan0 := false
					for _, network := range networks {
						if network.Name == "macvlan0" {
							hasMacVlan0 = true
						}
					}

					if hasMacVlan0 {
						fmt.Printf("Found a macvlan0 network, this is probably the one you want to use...\n")
					} else {
						var shouldCreateMacVlan0 bool
						if strings.Contains(dockerHost, "colima") {
							fmt.Printf("This appears to be colima, so auto-suggesting macvlan0 network.\n")
							shouldCreateMacVlan0 = true
						} else {
							fmt.Printf("This does not appear to be colima, so not auto-suggesting macvlan0 network.\n")
							shouldCreateMacVlan0 = false
						}

						shouldCreateMacVlan0 = readBool("Should we auto-create a colima macvlan0 network?", shouldCreateMacVlan0)
						if shouldCreateMacVlan0 {
							fmt.Printf("Creating macvlan0 network.\n")
							_, err := dockerCli.NetworkCreate(ctx, "macvlan0", types.NetworkCreate{
								Driver: "macvlan",
								IPAM: &network.IPAM{
									Driver: "default",
									Config: []network.IPAMConfig{
										{
											Subnet:  "192.168.106.0/24",
											IPRange: "192.168.106.128/25",
											Gateway: "192.168.106.1",
										},
									},
								},
								Options: map[string]string{
									"parent": "col0",
								},
							})
							if err != nil {
								fmt.Printf("Looks like something went wrong creating that network:\n%s\n", err)
							} else {
								fmt.Printf("Autocreation of the network succeeded!\n")
								hasMacVlan0 = true
							}
						}
					}

					if dockerNetwork == "" {
						if hasMacVlan0 {
							dockerNetwork = "macvlan0"
						}
					}
					if dockerNetwork == "" {
						dockerNetwork = "default"
					}

					dockerNetwork = readString(
						"What docker network should we use?",
						dockerNetwork, false)
				}
				if dockerNetwork == "" {
					fmt.Printf("The docker network to use is a required field\n")
					dockerEnabled = false
					continue
				}

				break
			}

			curConfig.Docker.Enabled.Set(dockerEnabled)
			curConfig.Docker.Host = dockerHost
			curConfig.Docker.Network = dockerNetwork
			curConfig.Docker.ForwardOnly.Set(false)
			saveConfig()
		}

		printAwsConfig := func() {
			fmt.Printf("  Enabled: %t\n", curConfig.AWS.Enabled.Value())
			fmt.Printf("  Region: %s\n", curConfig.AWS.Region)
		}
		{
			flagDisableAws, _ := cmd.Flags().GetBool("disable-aws")
			flagAwsRegion, _ := cmd.Flags().GetString("aws-region")
			envAwsRegion := os.Getenv("AWS_REGION")

			awsEnabled := true
			awsRegion := ""

			for {
				if flagDisableAws {
					fmt.Printf("AWS disabled via flags.\n")
					awsEnabled = false
					break
				}

				awsEnabled = readBool(
					"Would you like to enable AWS?",
					awsEnabled)
				if !awsEnabled {
					break
				}

				if flagAwsRegion != "" {
					fmt.Printf("AWS region specified via flags:\n  %s\n", flagAwsRegion)
					awsRegion = flagAwsRegion
				} else {
					if awsRegion == "" && awsCloudIdent != nil {
						fmt.Printf("Defaulting to aws region from environment.\n")
						awsRegion = awsCloudIdent.Region
					}
					if awsRegion == "" && envAwsRegion != "" {
						fmt.Printf("Defaulting to aws region from self-ident.\n")
						awsRegion = envAwsRegion
					}
					if awsRegion == "" {
						awsRegion = cbdcconfig.DEFAULT_AWS_REGION
					}

					awsRegion = readString(
						"What AWS region should we use?",
						awsRegion, false)
				}
				if awsRegion == "" {
					fmt.Printf("The AWS region is required.\n")
					awsEnabled = false
					continue
				}

				awsCfg, err := config.LoadDefaultConfig(ctx)
				if err != nil {
					fmt.Printf("Failed to loads AWS environment: %s\n", err)
					awsEnabled = false
					continue
				}

				ec2Client := ec2.New(ec2.Options{
					Region:      awsRegion,
					Credentials: awsCfg.Credentials,
				})

				_, err = ec2Client.DescribeInstances(ctx, nil)
				if err != nil {
					fmt.Printf("Failed to execute command (list instances) with AWS credentials: %s\n", err)
					awsEnabled = false
					continue
				}

				break
			}

			curConfig.AWS.Enabled.Set(awsEnabled)
			curConfig.AWS.Region = awsRegion
			saveConfig()
		}

		printAzureConfig := func() {
			fmt.Printf("  Enabled: %t\n", curConfig.Azure.Enabled.Value())
			fmt.Printf("  Region: %s\n", curConfig.Azure.Region)
		}
		{
			flagDisableAzure, _ := cmd.Flags().GetBool("disable-azure")
			flagAzureRegion, _ := cmd.Flags().GetString("azure-region")
			flagAzureSubID, _ := cmd.Flags().GetString("azure-sub-id")
			flagAzureRgName, _ := cmd.Flags().GetString("azure-rg-name")
			envAzureRegion := os.Getenv("AZURE_REGION")
			envAzureSubID := os.Getenv("AZURE_SUBSCRIPTION_ID")
			envAzureRgName := os.Getenv("AZURE_GROUP")

			azureEnabled := true
			azureRegion := ""
			azureSubID := ""
			azureRGName := ""

			for {
				if flagDisableAzure {
					fmt.Printf("Azure disabled via flags.\n")
					azureEnabled = false
					break
				}

				azureEnabled = readBool(
					"Would you like to enable Azure?",
					azureEnabled)
				if !azureEnabled {
					break
				}

				if flagAzureRegion != "" {
					fmt.Printf("Azure region specified via flags:\n  %s\n", flagAzureRegion)
					azureRegion = flagAzureRegion
				} else {
					if azureRegion == "" && envAzureRegion != "" {
						fmt.Printf("Defaulting to azure region from environment.\n")
						azureRegion = envAzureRegion
					}
					if azureRegion == "" && azureCloudIdent != nil {
						fmt.Printf("Defaulting to azure region from self-ident.\n")
						azureRegion = azureCloudIdent.Region
					}
					if azureRegion == "" {
						azureRegion = cbdcconfig.DEFAULT_AZURE_REGION
					}

					azureRegion = readString(
						"What Azure region should we use?",
						azureRegion, false)
				}
				if azureRegion == "" {
					fmt.Printf("The Azure Region is required.\n")
					azureEnabled = false
					continue
				}

				if flagAzureSubID != "" {
					fmt.Printf("Azure subscription id specified via flags:\n  %s\n", flagAzureSubID)
					azureSubID = flagAzureSubID
				} else {
					if azureSubID == "" && envAzureSubID != "" {
						fmt.Printf("Defaulting to azure subcription id from environment.\n")
						azureSubID = envAzureSubID
					}
					if azureSubID == "" && azureCloudIdent != nil {
						vmResInfo, _ := arm.ParseResourceID(azureCloudIdent.VmID)
						if vmResInfo != nil {
							fmt.Printf("Defaulting to azure subcription id from self-ident.\n")
							azureSubID = vmResInfo.SubscriptionID
						}
					}

					azureSubID = readString(
						"What Azure subscription id should we use?",
						azureSubID, false)
				}
				if azureSubID == "" {
					fmt.Printf("The Azure subscription id is required.\n")
					azureEnabled = false
					continue
				}

				if flagAzureRgName != "" {
					fmt.Printf("Azure resource group specified via flags:\n  %s\n", flagAzureRgName)
					azureRGName = flagAzureRgName
				} else {
					if azureRGName == "" && envAzureRgName != "" {
						fmt.Printf("Defaulting to azure resource group from environment.\n")
						azureRGName = envAzureRgName
					}
					if azureRGName == "" && azureCloudIdent != nil {
						vmResInfo, _ := arm.ParseResourceID(azureCloudIdent.VmID)
						if vmResInfo != nil {
							fmt.Printf("Defaulting to azure resource group from self-ident.\n")
							azureRGName = vmResInfo.ResourceGroupName
						}
					}

					azureRGName = readString(
						"What Azure resource group should we use?",
						azureRGName, false)
				}
				if azureRGName == "" {
					fmt.Printf("The Azure resource group is required.\n")
					azureEnabled = false
					continue
				}

				azureCreds, err := azidentity.NewDefaultAzureCredential(nil)
				if err != nil {
					fmt.Printf("Failed to loads Azure environment credentials: %s\n", err)
					azureEnabled = false
					continue
				}

				computeClient, err := armcompute.NewVirtualMachinesClient(azureSubID, azureCreds, nil)
				if err == nil {
					pager := computeClient.NewListPager(azureRGName, nil)
					_, err = pager.NextPage(ctx)
				}
				if err != nil {
					fmt.Printf("Failed to execute command (list vms) with Azure credentials: %s\n", err)
					azureEnabled = false
					continue
				}

				break
			}

			curConfig.Azure.Enabled.Set(azureEnabled)
			curConfig.Azure.Region = azureRegion
			curConfig.Azure.SubID = azureSubID
			curConfig.Azure.RGName = azureRGName
			saveConfig()
		}

		printGcpConfig := func() {
			fmt.Printf("  Not Yet Supported\n")
		}
		{
			curConfig.GCP.Enabled.Set(false)
			saveConfig()
		}

		printCapellaConfig := func() {
			fmt.Printf("  Enabled: %t\n", curConfig.Capella.Enabled.Value())
			fmt.Printf("  Endpoint: %s\n", curConfig.Capella.Endpoint)
			fmt.Printf("  Username: %s\n", curConfig.Capella.Username)
			fmt.Printf("  Password: %s\n", strings.Repeat("*", len(curConfig.Capella.Password)))
			fmt.Printf("  Organization ID: %s\n", curConfig.Capella.OrganizationID)
			fmt.Printf("  Default Cloud: %s\n", curConfig.Capella.DefaultCloud)
			fmt.Printf("  Default AWS Region: %s\n", curConfig.Capella.DefaultAwsRegion)
			fmt.Printf("  Default Azure Region: %s\n", curConfig.Capella.DefaultAzureRegion)
			fmt.Printf("  Default GCP Region: %s\n", curConfig.Capella.DefaultGcpRegion)
		}
		{
			flagDisableCapella, _ := cmd.Flags().GetBool("disable-capella")
			flagCapellaEndpoint, _ := cmd.Flags().GetString("capella-endpoint")
			flagCapellaUser, _ := cmd.Flags().GetString("capella-user")
			flagCapellaPass, _ := cmd.Flags().GetString("capella-pass")
			flagCapellaOid, _ := cmd.Flags().GetString("capella-oid")
			flagCapellaProvider, _ := cmd.Flags().GetString("capella-provider")
			flagCapellaAwsRegion, _ := cmd.Flags().GetString("capella-aws-region")
			flagCapellaAzureRegion, _ := cmd.Flags().GetString("capella-azure-region")
			flagCapellaGcpRegion, _ := cmd.Flags().GetString("capella-gcp-region")
			envCapellaEndpoint := os.Getenv("CAPELLA_ENDPOINT")
			envCapellaUser := os.Getenv("CAPELLA_USER")
			envCapellaPass := os.Getenv("CAPELLA_PASS")
			envCapellaOid := os.Getenv("CAPELLA_OID")

			capellaEnabled := true
			capellaEndpoint := ""
			capellaUser := ""
			capellaPass := ""
			capellaOid := ""
			capellaProvider := ""
			capellaAwsRegion := ""
			capellaAzureRegion := ""
			capellaGcpRegion := ""

			for {
				if flagDisableCapella {
					fmt.Printf("Capella disabled via flags.\n")
					capellaEnabled = false
					break
				}

				capellaEnabled = readBool(
					"Would you like to enable Capella?",
					capellaEnabled)
				if !capellaEnabled {
					break
				}

				if flagCapellaEndpoint != "" {
					fmt.Printf("Capella endpoint specified via flags:\n  %s\n", flagCapellaEndpoint)
					capellaEndpoint = flagCapellaEndpoint
				} else {
					if capellaEndpoint == "" && envCapellaEndpoint != "" {
						fmt.Printf("Defaulting to capella endpoint from environment.\n")
						capellaEndpoint = envCapellaEndpoint
					}
					if capellaEndpoint == "" {
						capellaEndpoint = cbdcconfig.DEFAULT_CAPELLA_ENDPOINT
					}

					capellaEndpoint = readString(
						"What Capella endpoint should we use?",
						capellaEndpoint, false)
				}
				if capellaEndpoint == "" {
					fmt.Printf("Capella endpoint is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaUser != "" {
					fmt.Printf("Capella user specified via flags:\n  %s\n", flagCapellaUser)
					capellaUser = flagCapellaUser
				} else {
					if capellaUser == "" && envCapellaUser != "" {
						fmt.Printf("Defaulting to capella user from environment.\n")
						capellaUser = envCapellaUser
					}

					capellaUser = readString(
						"What Capella user should we use?",
						capellaUser, false)
				}
				if capellaUser == "" {
					fmt.Printf("Capella user is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaPass != "" {
					fmt.Printf("Capella pass specified via flags.\n")
					capellaPass = flagCapellaPass
				} else {
					if capellaPass == "" && envCapellaPass != "" {
						fmt.Printf("Defaulting to capella pass from environment.\n")
						capellaPass = envCapellaPass
					}

					capellaPass = readString(
						"What Capella user should we use?",
						capellaPass, true)
				}
				if capellaPass == "" {
					fmt.Printf("Capella pass is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaOid != "" {
					fmt.Printf("Capella oid specified via flags:\n  %s\n", flagCapellaOid)
					capellaOid = flagCapellaOid
				} else {
					if capellaOid == "" && envCapellaOid != "" {
						fmt.Printf("Defaulting to capella OID from environment.\n")
						capellaOid = envCapellaOid
					}

					capellaOid = readString(
						"What Capella OID should we use?",
						capellaOid, false)
				}
				if capellaOid == "" {
					fmt.Printf("Capella oid is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaProvider != "" {
					fmt.Printf("Capella default provider specified via flags:\n  %s\n", flagCapellaProvider)
					capellaProvider = flagCapellaProvider
				} else {
					if capellaProvider == "" {
						// default to one of the enabled providers for locality
						if curConfig.AWS.Enabled.Value() {
							capellaProvider = "aws"
						} else if curConfig.Azure.Enabled.Value() {
							capellaProvider = "azure"
						} else if curConfig.GCP.Enabled.Value() {
							capellaProvider = "gcp"
						}
					}
					if capellaProvider == "" {
						capellaProvider = cbdcconfig.DEFAULT_CAPELLA_PROVIDER
					}

					capellaProvider = readString(
						"What Capella default provider should we use?",
						capellaProvider, false)
				}
				if capellaProvider == "" {
					fmt.Printf("Capella default provider is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaAwsRegion != "" {
					fmt.Printf("Capella default AWS region specified via flags:\n  %s\n", flagCapellaAwsRegion)
					capellaAwsRegion = flagCapellaAwsRegion
				} else {
					if capellaAwsRegion == "" && curConfig.AWS.Region != "" {
						capellaAwsRegion = curConfig.AWS.Region
					}
					if capellaAwsRegion == "" {
						capellaAwsRegion = cbdcconfig.DEFAULT_AWS_REGION
					}

					capellaAwsRegion = readString(
						"What Capella default AWS region should we use?",
						capellaAwsRegion, false)
				}
				if capellaAwsRegion == "" {
					fmt.Printf("Capella default AWS region is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaAzureRegion != "" {
					fmt.Printf("Capella default azure region specified via flags:\n  %s\n", flagCapellaAzureRegion)
					capellaAzureRegion = flagCapellaAzureRegion
				} else {
					if capellaAzureRegion == "" && curConfig.Azure.Region != "" {
						capellaAzureRegion = curConfig.Azure.Region
					}
					if capellaAzureRegion == "" {
						capellaAzureRegion = cbdcconfig.DEFAULT_AZURE_REGION
					}

					capellaAzureRegion = readString(
						"What Capella default Azure region should we use?",
						capellaAzureRegion, false)
				}
				if capellaAzureRegion == "" {
					fmt.Printf("Capella default Azure region is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaGcpRegion != "" {
					fmt.Printf("Capella default GCP region specified via flags:\n  %s\n", flagCapellaGcpRegion)
					capellaGcpRegion = flagCapellaGcpRegion
				} else {
					if capellaGcpRegion == "" && curConfig.GCP.Region != "" {
						capellaGcpRegion = curConfig.GCP.Region
					}
					if capellaGcpRegion == "" {
						capellaGcpRegion = cbdcconfig.DEFAULT_GCP_REGION
					}

					capellaGcpRegion = readString(
						"What Capella default GCP region should we use?",
						capellaGcpRegion, false)
				}
				if capellaGcpRegion == "" {
					fmt.Printf("Capella default GCP region is required.\n")
					capellaEnabled = false
					continue
				}

				break
			}

			curConfig.Capella.Enabled.Set(capellaEnabled)
			curConfig.Capella.Endpoint = capellaEndpoint
			curConfig.Capella.Username = capellaUser
			curConfig.Capella.Password = capellaPass
			curConfig.Capella.OrganizationID = capellaOid
			curConfig.Capella.DefaultCloud = capellaProvider
			curConfig.Capella.DefaultAwsRegion = capellaAwsRegion
			curConfig.Capella.DefaultAzureRegion = capellaAzureRegion
			curConfig.Capella.DefaultGcpRegion = capellaGcpRegion
			saveConfig()
		}

		printBaseConfig := func() {
			fmt.Printf("  Default Deployer: %s\n", curConfig.DefaultDeployer)
		}
		{
			fmt.Printf("-- Base Configuration\n")

			defaultDeployer := curConfig.DefaultDeployer
			if defaultDeployer == "" && curConfig.Docker.Enabled.Value() {
				defaultDeployer = "docker"
			}
			if defaultDeployer == "" && curConfig.Capella.Enabled.Value() {
				defaultDeployer = "cloud"
			}

			curConfig.DefaultDeployer = defaultDeployer
			saveConfig()
		}

		fmt.Printf("Initialization completed!\n")

		fmt.Printf("Using GitHub configuration:\n")
		printGitHubConfig()

		fmt.Printf("Using Docker configuration:\n")
		printDockerConfig()

		fmt.Printf("Using AWS configuration:\n")
		printAwsConfig()

		fmt.Printf("Using Azure configuration:\n")
		printAzureConfig()

		fmt.Printf("Using GCP configuration:\n")
		printGcpConfig()

		fmt.Printf("Using Capella configuration:\n")
		printCapellaConfig()

		fmt.Printf("Using Base configuration:\n")
		printBaseConfig()
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().Bool("auto", false, "Automatically setup without any interactivity")
	initCmd.Flags().Bool("disable-docker", false, "Disable Docker")
	initCmd.Flags().String("docker-host", "", "Docker host address to use")
	initCmd.Flags().String("docker-network", "", "Docker network to use")
	initCmd.Flags().Bool("disable-github", false, "Disable GitHub")
	initCmd.Flags().String("github-token", "", "GitHub token to use")
	initCmd.Flags().String("github-user", "", "GitHub user to use")
	initCmd.Flags().Bool("disable-capella", false, "Disable Capella")
	initCmd.Flags().String("capella-endpoint", "", "Capella endpoint to use")
	initCmd.Flags().String("capella-user", "", "Capella user to use")
	initCmd.Flags().String("capella-pass", "", "Capella pass to use")
	initCmd.Flags().String("capella-oid", "", "Capella organization id to use")
	initCmd.Flags().String("capella-provider", "", "Capella default cloud provider to use")
	initCmd.Flags().String("capella-aws-region", "", "Capella default AWS region to use")
	initCmd.Flags().String("capella-azure-region", "", "Capella default Azure region to use")
	initCmd.Flags().String("capella-gcp-region", "", "Capella default GCP region to use")
	initCmd.Flags().Bool("disable-aws", false, "Disable AWS")
	initCmd.Flags().String("aws-region", "", "AWS default region to use")
	initCmd.Flags().Bool("disable-azure", false, "Disable Azure")
}
