package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"runtime"
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
	"github.com/couchbaselabs/cbdinocluster/utils/caocontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cloudinstancecontrol"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/go-github/v53/github"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

const (
	DefaultCaoVersion = "2.6.0"
)

var initCmd = &cobra.Command{
	Use:   "init [flags]",
	Short: "Initializes the tool",
	Run: func(cmd *cobra.Command, args []string) {
		loggerConfig := zap.NewDevelopmentConfig()
		loggerConfig.DisableStacktrace = true
		loggerConfig.DisableCaller = true
		loggerConfig.EncoderConfig.TimeKey = ""

		logger, _ := loggerConfig.Build()
		ctx := context.Background()

		autoConfig, _ := cmd.Flags().GetBool("auto")
		nociConfig, _ := cmd.Flags().GetBool("noci")
		ciConfig := autoConfig && !nociConfig

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

		readDuration := func(q string, defaultValue time.Duration) time.Duration {
			for {
				str := readString(q, defaultValue.String(), false)

				dura, err := time.ParseDuration(str)
				if err != nil {
					fmt.Printf("Invalid entry, try again...\n")
					continue
				}

				return dura
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
			if runtime.GOOS == "windows" {
				fmt.Printf("not checked on windows.\n")
				return ""
			}

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
			type dockerSocketInfo struct {
				Scheme string
				Path   string
			}
			dockerSockets := []dockerSocketInfo{
				{
					Scheme: "unix",
					Path:   "/var/run/docker.sock",
				},
			}

			if runtime.GOOS == "windows" {
				dockerSockets = append(dockerSockets, dockerSocketInfo{
					Scheme: "npipe",
					Path:   "//./pipe/docker_engine",
				})
			} else if runtime.GOOS == "linux" {
				currentUser, err := user.Current()
				if err == nil {
					dockerSockets = append(dockerSockets, dockerSocketInfo{
						Scheme: "unix",
						Path:   filepath.Join("/run/user", currentUser.Uid, "podman/podman.sock"),
					})
				}
			}

			for _, socket := range dockerSockets {
				fmt.Printf("Checking for Docker installation at `%s`... ", socket.Path)
				if checkFileExists(socket.Path) {
					fmt.Printf("found.\n")
					return socket.Scheme + "://" + socket.Path
				}
				fmt.Printf("not found.\n")
			}

			return ""
		}

		getColimaAddress := func() string {
			fmt.Printf("Attempting to fetch colima instance data.\n")
			out, err := exec.Command("colima", "ls", "-j").Output()
			if err != nil {
				fmt.Printf("failed to execute colima: %s", err)
				return ""
			}

			var instance struct {
				Address string `json:"address"`
			}
			err = json.Unmarshal(out, &instance)
			if err != nil {
				fmt.Printf("failed to unmarshal colima response: %s", err)
				return ""
			}

			return instance.Address
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

			githubEnabled := curConfig.GitHub.Enabled.ValueOr(true)
			githubToken := curConfig.GitHub.Token
			githubUser := curConfig.GitHub.User

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

			dockerEnabled := curConfig.Docker.Enabled.ValueOr(true)
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

					hasDinoNet := false
					for _, network := range networks {
						if network.Name == "dinonet" {
							hasDinoNet = true
						}
					}

					if hasDinoNet {
						fmt.Printf("Found a dinonet network, this is probably the one you want to use...\n")
					} else {
						if !strings.Contains(dockerHost, "colima") {
							fmt.Printf("This does not appear to be colima, cannot auto-create dinonet network.\n")
						} else {
							fmt.Printf("This appears to be colima, attempting to identify network information.\n")

							colimaAddress := getColimaAddress()
							fmt.Printf("Identified colima address of `%s`\n", colimaAddress)

							colimaIP := net.ParseIP(colimaAddress).To4()
							if colimaIP == nil {
								fmt.Printf("Network identification failed, cannot auto-create dinonet network...")
							} else {
								subnet := fmt.Sprintf("%d.%d.%d.0/24", colimaIP[0], colimaIP[1], colimaIP[2])
								ipRange := fmt.Sprintf("%d.%d.%d.128/25", colimaIP[0], colimaIP[1], colimaIP[2])
								gateway := fmt.Sprintf("%d.%d.%d.1", colimaIP[0], colimaIP[1], colimaIP[2])

								shouldCreateDinoNet := readBool("Should we auto-create a colima dinonet network?", true)
								if shouldCreateDinoNet {
									fmt.Printf("Creating dinonet network (subnet: %s, %s, %s).\n",
										subnet, ipRange, gateway)

									_, err := dockerCli.NetworkCreate(ctx, "dinonet", types.NetworkCreate{
										Driver: "ipvlan",
										IPAM: &network.IPAM{
											Driver: "default",
											Config: []network.IPAMConfig{
												{
													Subnet:  subnet,
													IPRange: ipRange,
													Gateway: gateway,
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
										hasDinoNet = true
										dockerNetwork = "dinonet"
									}
								}
							}
						}
					}

					if dockerNetwork == "" {
						if hasDinoNet {
							dockerNetwork = "dinonet"
						}
					}
					if dockerNetwork == "" {
						dockerNetwork = "bridge"
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

		printK8sConfig := func() {
			fmt.Printf("  Enabled: %t\n", curConfig.K8s.Enabled.Value())
			fmt.Printf("  CaoTools: %s\n", curConfig.K8s.CaoTools)
			fmt.Printf("  KubeConfig: %s\n", curConfig.K8s.KubeConfig)
			fmt.Printf("  Context: %s\n", curConfig.K8s.Context)
		}
		{
			fmt.Printf("-- K8s Configuration\n")

			flagDisableK8s, _ := cmd.Flags().GetBool("disable-k8s")
			flagCaoTools, _ := cmd.Flags().GetString("cao-tools")
			flagKubeConfig, _ := cmd.Flags().GetString("kube-config")
			flagKubeContext, _ := cmd.Flags().GetString("kube-context")
			envKubeConfig := os.Getenv("KUBECONFIG")

			k8sEnabled := curConfig.K8s.Enabled.ValueOr(true)
			k8sCaoTools := curConfig.K8s.CaoTools
			k8sKubeConfig := curConfig.K8s.KubeConfig
			k8sKubeContext := curConfig.K8s.Context

			for {
				if flagDisableK8s {
					fmt.Printf("K8s disabled via flags.\n")
					k8sEnabled = false
					break
				}

				k8sEnabled = readBool(
					"Would you like to configure K8s Deployments?",
					k8sEnabled)
				if !k8sEnabled {
					break
				}

				var kubeConfig *api.Config
				if flagKubeConfig != "" {
					kubeConfig, err = clientcmd.LoadFromFile(flagKubeConfig)
					if err != nil {
						fmt.Printf("Failed to load kube-config file from flag:\n  %s\n", err)
						k8sEnabled = false
						continue
					}

					k8sKubeConfig = flagKubeConfig
				} else if envKubeConfig != "" {
					kubeConfig, err = clientcmd.LoadFromFile(envKubeConfig)
					if err != nil {
						fmt.Printf("Failed to load kube-config file from env:\n  %s\n", err)
						k8sEnabled = false
						continue
					}

					k8sKubeConfig = envKubeConfig
				} else {
					kubeConfig, err = clientcmd.LoadFromFile(clientcmd.RecommendedHomeFile)
					if err != nil {
						fmt.Printf("Failed to load kube-config file from default path:\n  %s\n", err)
						k8sEnabled = false
						continue
					}

					k8sKubeConfig = clientcmd.RecommendedHomeFile
				}

				if flagKubeContext != "" {
					if _, ok := kubeConfig.Contexts[flagKubeContext]; !ok {
						fmt.Printf("Failed to find kube-context from flag:\n  %s\n", err)
						k8sEnabled = false
						continue
					}

					k8sKubeContext = flagKubeContext
				} else {
					fmt.Printf("Listing k8s contexts:\n")
					for contextName := range kubeConfig.Contexts {
						fmt.Printf("  %s\n", contextName)
					}

					fmt.Printf("Current k8s context: %s\n", kubeConfig.CurrentContext)

					k8sKubeContext = kubeConfig.CurrentContext

					k8sKubeContext = readString(
						"What k8s context should we use?",
						k8sKubeContext, false)

					if _, ok := kubeConfig.Contexts[k8sKubeContext]; !ok {
						fmt.Printf("Failed to find specified context:\n  %s\n", err)
						k8sEnabled = false
						continue
					}
				}

				if flagCaoTools != "" {
					fmt.Printf("CAO tools path specified via flags:\n  %s\n", flagCaoTools)
					k8sCaoTools = flagCaoTools
				} else {
					dinoCaoPath := path.Join(userHomePath, ".dinotools/cao", DefaultCaoVersion)

					_, err := os.Stat(dinoCaoPath)
					if err == nil {
						fmt.Printf("Found existing dinocluster cao installation: %s\n", dinoCaoPath)
						k8sCaoTools = dinoCaoPath
					} else {
						fmt.Printf("In order to use kubernetes, we need a local install of the cao tools.\n")

						shouldInstallCaoTools := readBool("Should we auto-install the cao tools?", true)
						if shouldInstallCaoTools {
							err := caocontrol.DownloadLocalCaoTools(ctx, logger, dinoCaoPath, DefaultCaoVersion, false)
							if err != nil {
								fmt.Printf("Failed to install cao tools\n  %s\n", err)
							} else {
								k8sCaoTools = dinoCaoPath
							}
						}
					}

					k8sCaoTools = readString(
						"What CAO Tools path should we use?",
						k8sCaoTools, false)
				}
				if k8sCaoTools == "" {
					fmt.Printf("The CAO tools path is required.\n")
					k8sEnabled = false
					continue
				}

				caoCtrl, err := caocontrol.NewController(&caocontrol.ControllerOptions{
					Logger:         logger,
					CaoToolsPath:   k8sCaoTools,
					KubeConfigPath: k8sKubeConfig,
					KubeContext:    k8sKubeContext,
					GhcrUser:       curConfig.GitHub.User,
					GhcrToken:      curConfig.GitHub.Token,
				})
				if err != nil {
					fmt.Printf("Failed to initialize cao system:\n  %s\n", err)
					k8sEnabled = false
					continue
				}

				err = caoCtrl.Ping(ctx)
				if err != nil {
					fmt.Printf("Failed to check connectivity to k8s:\n  %s\n", err)
					k8sEnabled = false
					continue
				}

				hasExistingCrds, err := caoCtrl.IsCrdInstalled(ctx)
				if err != nil {
					fmt.Printf("Failed to check for existing CAO CRDs:\n  %s\n", err)
					k8sEnabled = false
					continue
				}

				var shouldInstallCrds bool
				if hasExistingCrds {
					fmt.Printf("We found existing CAO CRDs.\n")
					shouldInstallCrds = false
				} else {
					fmt.Printf("It does not appear that the CAO CRDs are installed.\n")
					shouldInstallCrds = true
				}

				shouldInstallCrds = readBool("Should we auto-install the cao crds?", shouldInstallCrds)

				if shouldInstallCrds {
					fmt.Printf("---- crd install start ----\n")
					err := caoCtrl.InstallDefaultCrd(ctx)
					fmt.Printf("---- crd install end ----\n")
					if err != nil {
						fmt.Printf("Failed to install CAO CRDs:\n  %s\n", err)
						k8sEnabled = false
						continue
					}
				}

				admNamespace, err := caoCtrl.FindAdmissionController(ctx)
				if err != nil {
					fmt.Printf("Failed to check for existing Admission Controller:\n  %s\n", err)
					k8sEnabled = false
					continue
				}

				var shouldInstallAdm bool
				if admNamespace != "" {
					fmt.Printf("We found an existing Admission Controller.\n")
					shouldInstallAdm = false
				} else {
					fmt.Printf("It does not appear that the Admission Controller is installed.\n")
					shouldInstallAdm = true
				}

				shouldInstallAdm = readBool("Should we auto-install the Admission Controller?", shouldInstallAdm)

				if shouldInstallAdm {
					fmt.Printf("---- admission controller install start ----\n")
					err := caoCtrl.InstallGlobalAdmissionController(ctx, "", "")
					fmt.Printf("---- admission controller install end ----\n")
					if err != nil {
						fmt.Printf("Failed to install Admission Controller:\n  %s\n", err)
						k8sEnabled = false
						continue
					}
				}

				break
			}

			curConfig.K8s.Enabled.Set(k8sEnabled)
			curConfig.K8s.CaoTools = k8sCaoTools
			curConfig.K8s.KubeConfig = k8sKubeConfig
			curConfig.K8s.Context = k8sKubeContext
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

			awsEnabled := curConfig.AWS.Enabled.ValueOr(true)
			awsRegion := curConfig.AWS.Region

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

			azureEnabled := curConfig.Azure.Enabled.ValueOr(true)
			azureRegion := curConfig.Azure.Region
			azureSubID := curConfig.Azure.SubID
			azureRGName := curConfig.Azure.RGName

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
			fmt.Printf("  Override Token: %s\n", strings.Repeat("*", len(curConfig.Capella.OverrideToken)))
			fmt.Printf("  Internal Support Token: %s\n", strings.Repeat("*", len(curConfig.Capella.InternalSupportToken)))
			fmt.Printf("  Default Cloud: %s\n", curConfig.Capella.DefaultCloud)
			fmt.Printf("  Default AWS Region: %s\n", curConfig.Capella.DefaultAwsRegion)
			fmt.Printf("  Default Azure Region: %s\n", curConfig.Capella.DefaultAzureRegion)
			fmt.Printf("  Default GCP Region: %s\n", curConfig.Capella.DefaultGcpRegion)
			fmt.Printf("  Host name to upload server logs: %s\n", curConfig.Capella.UploadServerLogsHostName)
		}
		{
			flagDisableCapella, _ := cmd.Flags().GetBool("disable-capella")
			flagCapellaEndpoint, _ := cmd.Flags().GetString("capella-endpoint")
			flagCapellaUser, _ := cmd.Flags().GetString("capella-user")
			flagCapellaPass, _ := cmd.Flags().GetString("capella-pass")
			flagCapellaOid, _ := cmd.Flags().GetString("capella-oid")
			flagCapellaOverrideToken, _ := cmd.Flags().GetString("capella-override-token")
			flagCapellaInternalSupportToken, _ := cmd.Flags().GetString("capella-internal-support-token")
			flagUploadServerLogsHostName, _ := cmd.Flags().GetString("upload-server-logs-host-name")
			flagCapellaProvider, _ := cmd.Flags().GetString("capella-provider")
			flagCapellaAwsRegion, _ := cmd.Flags().GetString("capella-aws-region")
			flagCapellaAzureRegion, _ := cmd.Flags().GetString("capella-azure-region")
			flagCapellaGcpRegion, _ := cmd.Flags().GetString("capella-gcp-region")
			envCapellaEndpoint := os.Getenv("CAPELLA_ENDPOINT")
			envCapellaUser := os.Getenv("CAPELLA_USER")
			envCapellaPass := os.Getenv("CAPELLA_PASS")
			envCapellaOid := os.Getenv("CAPELLA_OID")
			envCapellaOverrideToken := os.Getenv("CAPELLA_OVERRIDE_TOKEN")
			envCapellaInternalSupportToken := os.Getenv("CAPELLA_INTERNAL_SUPPORT_TOKEN")

			capellaEnabled := curConfig.Capella.Enabled.ValueOr(true)
			capellaEndpoint := curConfig.Capella.Endpoint
			capellaUser := curConfig.Capella.Username
			capellaPass := curConfig.Capella.Password
			capellaOid := curConfig.Capella.OrganizationID
			capellaOverrideToken := curConfig.Capella.OverrideToken
			capellaInternalSupportToken := curConfig.Capella.InternalSupportToken
			UploadServerLogsHostName := curConfig.Capella.UploadServerLogsHostName
			capellaProvider := curConfig.Capella.DefaultCloud
			capellaAwsRegion := curConfig.Capella.DefaultAwsRegion
			capellaAzureRegion := curConfig.Capella.DefaultAzureRegion
			capellaGcpRegion := curConfig.Capella.DefaultGcpRegion

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

				if flagCapellaOverrideToken != "" {
					fmt.Printf("Capella override token specified via flags:\n  %s\n", flagCapellaOverrideToken)
					capellaOverrideToken = flagCapellaOverrideToken
				} else {
					if capellaOverrideToken == "" && envCapellaOverrideToken != "" {
						fmt.Printf("Defaulting to capella override token from environment.\n")
						capellaOverrideToken = envCapellaOverrideToken
					}

					capellaOverrideToken = readString(
						"What Capella Override token should we use?",
						capellaOverrideToken, true)
				}
				if capellaOverrideToken == "" {
					fmt.Printf("Capella override token is required.\n")
					capellaEnabled = false
					continue
				}

				if flagCapellaInternalSupportToken != "" {
					fmt.Printf("Capella internal support token specified via flags:\n  %s\n", flagCapellaInternalSupportToken)
					capellaInternalSupportToken = flagCapellaInternalSupportToken
				} else {
					if capellaInternalSupportToken == "" && envCapellaInternalSupportToken != "" {
						fmt.Printf("Defaulting to capella internal support token from environment.\n")
						capellaInternalSupportToken = envCapellaInternalSupportToken
					}

					capellaInternalSupportToken = readString(
						"What Capella internal support token should we use?",
						capellaInternalSupportToken, true)
				}
				if capellaInternalSupportToken == "" {
					fmt.Printf("Capella internal support token is required for functionalities like update server version, collect server logs\n")
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

				if flagUploadServerLogsHostName != "" {
					fmt.Printf("Upload server logs host name specified via flag:\n  %s\n", flagUploadServerLogsHostName)
					UploadServerLogsHostName = flagUploadServerLogsHostName
				} else {
					UploadServerLogsHostName = readString(
						"What Host name should we use to upload server logs?",
						UploadServerLogsHostName, false)
				}
				if UploadServerLogsHostName == "" {
					fmt.Printf("host name is required to get server logs for a capella cluster\n")
				}

				break
			}

			curConfig.Capella.Enabled.Set(capellaEnabled)
			curConfig.Capella.Endpoint = capellaEndpoint
			curConfig.Capella.Username = capellaUser
			curConfig.Capella.Password = capellaPass
			curConfig.Capella.OrganizationID = capellaOid
			curConfig.Capella.OverrideToken = capellaOverrideToken
			curConfig.Capella.InternalSupportToken = capellaInternalSupportToken
			curConfig.Capella.DefaultCloud = capellaProvider
			curConfig.Capella.DefaultAwsRegion = capellaAwsRegion
			curConfig.Capella.DefaultAzureRegion = capellaAzureRegion
			curConfig.Capella.DefaultGcpRegion = capellaGcpRegion
			curConfig.Capella.UploadServerLogsHostName = UploadServerLogsHostName
			saveConfig()
		}

		printBaseConfig := func() {
			fmt.Printf("  Default Deployer: %s\n", curConfig.DefaultDeployer)
			fmt.Printf("  Default Expiry: %s\n", curConfig.DefaultExpiry.String())
		}
		{
			fmt.Printf("-- Base Configuration\n")

			{
				defaultDeployer := curConfig.DefaultDeployer
				if defaultDeployer == "" && curConfig.Docker.Enabled.Value() {
					defaultDeployer = "docker"
				}
				if defaultDeployer == "" && curConfig.Capella.Enabled.Value() {
					defaultDeployer = "cloud"
				}

				defaultDeployer = readString(
					"What deployer should we use by default?",
					defaultDeployer, false)

				curConfig.DefaultDeployer = defaultDeployer
			}

			{
				var defaultExpiry time.Duration
				if ciConfig {
					defaultExpiry = 1 * time.Hour
				}

				defaultExpiry = readDuration(
					"What cluster expiry should we use by default?",
					defaultExpiry)

				curConfig.DefaultExpiry = defaultExpiry
			}

			saveConfig()
		}

		fmt.Printf("Initialization completed!\n")

		fmt.Printf("Using GitHub configuration:\n")
		printGitHubConfig()

		fmt.Printf("Using Docker configuration:\n")
		printDockerConfig()

		fmt.Printf("Using K8s configuration:\n")
		printK8sConfig()

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
	initCmd.Flags().Bool("noci", false, "Disable CI mode when using --auto")
	initCmd.Flags().Bool("disable-docker", false, "Disable Docker")
	initCmd.Flags().String("docker-host", "", "Docker host address to use")
	initCmd.Flags().String("docker-network", "", "Docker network to use")
	initCmd.Flags().Bool("disable-k8s", false, "Disable K8s")
	initCmd.Flags().String("cao-tools", "", "CAO tools path to use")
	initCmd.Flags().String("kube-config", "", "Kubeconfig file to use")
	initCmd.Flags().String("k8s-context", "", "K8s context to use")
	initCmd.Flags().Bool("disable-github", false, "Disable GitHub")
	initCmd.Flags().String("github-token", "", "GitHub token to use")
	initCmd.Flags().String("github-user", "", "GitHub user to use")
	initCmd.Flags().Bool("disable-capella", false, "Disable Capella")
	initCmd.Flags().String("capella-endpoint", "", "Capella endpoint to use")
	initCmd.Flags().String("capella-user", "", "Capella user to use")
	initCmd.Flags().String("capella-pass", "", "Capella pass to use")
	initCmd.Flags().String("capella-oid", "", "Capella organization id to use")
	initCmd.Flags().String("capella-override-token", "", "Capella override token to use")
	initCmd.Flags().String("capella-internal-support-token", "", "Capella internal support token to use")
	initCmd.Flags().String("capella-provider", "", "Capella default cloud provider to use")
	initCmd.Flags().String("capella-aws-region", "", "Capella default AWS region to use")
	initCmd.Flags().String("capella-azure-region", "", "Capella default Azure region to use")
	initCmd.Flags().String("capella-gcp-region", "", "Capella default GCP region to use")
	initCmd.Flags().Bool("disable-aws", false, "Disable AWS")
	initCmd.Flags().String("aws-region", "", "AWS default region to use")
	initCmd.Flags().Bool("disable-azure", false, "Disable Azure")
}
