package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/go-github/v53/github"
	"github.com/spf13/cobra"
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
			curConfig = &cbdcconfig.Config{}
		}

		readString := func(defaultValue string) string {
			var str string
			fmt.Scanf("%s", &str)
			str = strings.TrimSpace(str)
			if str == "" {
				return defaultValue
			}
			return str
		}

		readBool := func(defaultValue bool) bool {
			for {
				boolStr := strings.ToLower(readString(""))
				if boolStr == "" {
					return defaultValue
				} else if boolStr == "y" || boolStr == "yes" {
					return true
				} else if boolStr == "n" || boolStr == "no" {
					return false
				}

				fmt.Printf("Invalid entry, try again: ")
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

		printDockerConfig := func() {
			fmt.Printf("  Host: %s\n", curConfig.Docker.Host)
			fmt.Printf("  Network: %s\n", curConfig.Docker.Network)
			fmt.Printf("  Forward Only: %t\n", curConfig.Docker.ForwardOnly)
		}
		{
			flagDockerHost, _ := cmd.Flags().GetString("docker-host")
			flagDockerNetwork, _ := cmd.Flags().GetString("docker-network")
			envDockerHost := os.Getenv("DOCKER_HOST")
			if flagDockerHost != "" || flagDockerNetwork != "" {
				curConfig.Docker = nil
			}
			if curConfig.Docker != nil {
				if autoConfig {
					// leave the original configuration
				} else {
					fmt.Printf("Found previous docker configuration:\n")
					printDockerConfig()
					fmt.Printf("Would you like to keep these settings? [Y/n]: ")
					if !readBool(true) {
						curConfig.Docker = nil
					}
				}
			}
			if curConfig.Docker == nil {
				for {
					curConfig.Docker = &cbdcconfig.Config_Docker{
						Host:        "",
						Network:     "",
						ForwardOnly: true,
					}

					dockerHost := ""
					isUsingColima := false
					if dockerHost == "" {
						if flagDockerHost != "" {
							fmt.Printf("Using flag as container engine\n")
							dockerHost = flagDockerHost
						}
					}
					if dockerHost == "" {
						if envDockerHost != "" {
							useEnvDocker := true
							if !autoConfig {
								fmt.Printf("It looks like $DOCKER_HOST was defined:\n")
								fmt.Printf("  DOCKER_HOST: %s\n", envDockerHost)
								fmt.Printf("Would you like to use this for containers? [Y/n]: ")
								useEnvDocker = readBool(true)
								fmt.Printf("Using $DOCKER_HOST as container engine\n")
							}
							if useEnvDocker {
								dockerHost = envDockerHost
							}
						}
					}
					if dockerHost == "" {
						colimaSocketPath := path.Join(userHomePath, "/.colima/default/docker.sock")
						hasColima := checkFileExists(colimaSocketPath)

						if hasColima {
							useColima := true
							if !autoConfig {
								fmt.Printf("It looks like you're using Colima!\n")
								fmt.Printf("Would you like to use Colima for containers? [Y/n]: ")
								useColima = readBool(true)
							}
							if useColima {
								fmt.Printf("Using colima as container engine\n")
								dockerHost = "unix://" + colimaSocketPath
								isUsingColima = true
							}
						}
					}
					if dockerHost == "" {
						dockerSocketPath := "/var/run/docker.sock"
						hasDocker := checkFileExists(dockerSocketPath)

						if hasDocker {
							useDocker := true
							if !autoConfig {
								fmt.Printf("It looks like you're using Docker!\n")
								fmt.Printf("Would you like to use Docker for containers? [Y/n]: ")
								useDocker = readBool(true)
							}
							if useDocker {
								fmt.Printf("Using docker as container engine\n")
								dockerHost = "unix://" + dockerSocketPath
							}
						}
					}
					if dockerHost == "" {
						if !autoConfig {
							fmt.Printf("We failed to auto-detect a container engine...\n")
							fmt.Printf("What docker host should we use? []: ")
							dockerHost = readString("")
						}
					}

					if dockerHost == "" {
						fmt.Printf("Docker support disabled.\n")
						break
					}

					fmt.Printf("Pinging the docker host to confirm it works...\n")
					dockerCli, err := client.NewClientWithOpts(
						client.WithHost(dockerHost),
						client.WithAPIVersionNegotiation(),
					)
					if err != nil {
						fmt.Printf("Failed to setup docker client:\n  %s\n", err)
						if autoConfig {
							fmt.Printf("Proceeding without docker due to auto mode...\n")
							break
						} else {
							fmt.Printf("Let's try to set up docker again...\n")
							continue
						}
					}

					_, err = dockerCli.Ping(ctx)
					if err != nil {
						fmt.Printf("Failed to ping docker:\n  %s\n", err)
						if autoConfig {
							fmt.Printf("Proceeding without docker due to auto mode...\n")
							break
						} else {
							fmt.Printf("Let's try to set up docker again...\n")
							continue
						}
					}

					fmt.Printf("Success!\n")

					// Determine if we have a macvlan0 already existing
					hasMacVlan0 := false
					networks, _ := dockerCli.NetworkList(ctx, types.NetworkListOptions{})
					for _, network := range networks {
						if network.Name == "macvlan0" {
							hasMacVlan0 = true
						}
					}

					// Mark if we need to use an alternate network in order to support multiple-containers
					needsAltNetwork := false
					if runtime.GOOS == "darwin" {
						needsAltNetwork = true
					} else if runtime.GOOS == "windows" {
						needsAltNetwork = true
					}

					dockerNetwork := ""
					dockerForwardOnly := true

					if dockerNetwork == "" {
						if hasMacVlan0 {
							useMacVlan0 := true
							if !autoConfig {
								fmt.Printf("We found a macvlan0 network, which is probably the correct one to use.\n")
								fmt.Printf("Did you want to use the macvlan0 network? [Y/n]: ")
								useMacVlan0 = readBool(true)
							}
							if useMacVlan0 {
								fmt.Printf("Using macvlan0 network.\n")
								dockerNetwork = "macvlan0"
								dockerForwardOnly = false
							}
						}
					}
					if dockerNetwork == "" {
						if isUsingColima && !hasMacVlan0 {
							autoCreateNetwork := true
							if !autoConfig {
								fmt.Printf("It looks like you're using Colima...")
								fmt.Printf("Did you want to create and use the macvlan0 network? [Y/n]: ")
								autoCreateNetwork = readBool(true)
							}
							if autoCreateNetwork {
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
									dockerNetwork = "macvlan0"
									dockerForwardOnly = false
								}
							}
						}
					}
					if dockerNetwork == "" {
						if !autoConfig {
							fmt.Printf("Which docker network should we use? [default]: ")
							dockerNetwork = readString("default")

							if !needsAltNetwork {
								fmt.Printf("Should we enable non-forward-based containers? [Y/n]: ")
								dockerForwardOnly = !readBool(true)
							} else {
								fmt.Printf("Should we enable non-forward-based containers? [y/N]: ")
								dockerForwardOnly = !readBool(false)
							}
						}
					}

					curConfig.Docker = &cbdcconfig.Config_Docker{
						Host:        dockerHost,
						Network:     dockerNetwork,
						ForwardOnly: dockerForwardOnly,
					}

					break
				}

				saveConfig()
			}
		}

		printGitHubConfig := func() {
			fmt.Printf("  Token: %s\n", strings.Repeat("*", len(curConfig.GitHub.Token)))
			fmt.Printf("  User: %s\n", curConfig.GitHub.User)
		}
		{
			flagGithubToken, _ := cmd.Flags().GetString("github-token")
			envGithubToken := os.Getenv("GITHUB_TOKEN")

			if flagGithubToken != "" {
				curConfig.GitHub = nil
			}
			if curConfig.GitHub != nil {
				if autoConfig {
					// leave the original configuration
				} else {
					fmt.Printf("Found previous GitHub configuration:\n")
					printGitHubConfig()
					fmt.Printf("Would you like to keep these settings? [Y/n]: ")
					if !readBool(true) {
						curConfig.GitHub = nil
					}
				}
			}
			if curConfig.GitHub == nil {
				for {
					curConfig.GitHub = &cbdcconfig.Config_GitHub{
						Token: "",
						User:  "",
					}

					githubToken := ""
					if githubToken == "" {
						if flagGithubToken != "" {
							fmt.Printf("Using github token from flag\n")
							githubToken = flagGithubToken
						}
					}
					if githubToken == "" {
						if envGithubToken != "" {
							useEnvToken := true
							if !autoConfig {
								fmt.Printf("It looks like $GITHUB_TOKEN was defined.\n")
								fmt.Printf("  GITHUB_TOKEN: %s\n", strings.Repeat("*", len(envGithubToken)))
								fmt.Printf("Would you like to use this? [Y/n]: ")
								useEnvToken = readBool(true)
							}
							if useEnvToken {
								fmt.Printf("Using github token from $GITHUB_TOKEN\n")
								githubToken = envGithubToken
							}
						}
					}
					if githubToken == "" {
						if !autoConfig {
							fmt.Printf("To access internal server builds hosted on the GitHub Package Registry,\n")
							fmt.Printf("please provide a GitHub personal access token with 'read:packages' scope.\n")
							fmt.Printf("What GitHub token should we use? []: ")
							githubToken = readString("")
						}
					}

					if githubToken == "" {
						fmt.Printf("GitHub support disabled.\n")
						break
					}

					ts := oauth2.StaticTokenSource(
						&oauth2.Token{AccessToken: githubToken},
					)
					tc := oauth2.NewClient(ctx, ts)

					githubClient := github.NewClient(tc)
					user, _, err := githubClient.Users.Get(ctx, "")
					if err != nil {
						fmt.Printf("Failed to fetch user details with provided token:\n  %s\n", err)
						if autoConfig {
							fmt.Printf("Proceeding without GitHub due to auto mode...\n")
							break
						} else {
							fmt.Printf("Let's try to set up docker again...\n")
							continue
						}
					}

					fmt.Printf("Authenticated to GitHub as '%s'\n", *user.Login)

					curConfig.GitHub = &cbdcconfig.Config_GitHub{
						Token: githubToken,
						User:  *user.Login,
					}

					break
				}

				saveConfig()
			}
		}

		printAwsConfig := func() {
			fmt.Printf("  From Environment: %t\n", curConfig.AWS.FromEnvironment)
			fmt.Printf("  Access Key: %s\n", curConfig.AWS.AccessKey)
			fmt.Printf("  Secret Key: %s\n", strings.Repeat("*", len(curConfig.AWS.SecretKey)))
		}
		{
			flagAwsUseEnv, _ := cmd.Flags().GetBool("aws-use-env")
			flagAwsAccessKey, _ := cmd.Flags().GetString("aws-access-key")
			flagAwsSecretKey, _ := cmd.Flags().GetString("aws-secret-key")

			if flagAwsUseEnv || flagAwsAccessKey != "" || flagAwsSecretKey != "" {
				curConfig.AWS = nil
			}
			if curConfig.AWS != nil {
				if autoConfig {
					// leave the original configuration
				} else {
					fmt.Printf("Found previous AWS configuration:\n")
					printAwsConfig()
					fmt.Printf("Would you like to keep these settings? [Y/n]: ")
					if !readBool(true) {
						curConfig.AWS = nil
					}
				}
			}
			if curConfig.AWS == nil {
				for {
					curConfig.AWS = &cbdcconfig.Config_AWS{
						FromEnvironment: false,
						AccessKey:       "",
						SecretKey:       "",
						Region:          "",
					}

					awsUseEnv := false
					awsToken := ""
					awsSecret := ""
					if awsToken == "" {
						if flagAwsAccessKey != "" || flagAwsSecretKey != "" {
							if flagAwsAccessKey == "" || flagAwsSecretKey == "" {
								fmt.Printf("Must specify both aws-access-key AND aws-secret-key, skipping...")
							} else {
								fmt.Printf("Using aws config from flag\n")
								awsUseEnv = false
								awsToken = flagAwsAccessKey
								awsSecret = flagAwsSecretKey
							}
						}
					}
					if awsToken == "" {
						var awsCreds *aws.Credentials

						fmt.Printf("Attempting to fetch credentials from AWS client...\n")

						cfg, err := config.LoadDefaultConfig(ctx)
						if err != nil {
							fmt.Printf("Failed to load default AWS config: %s\n", err)
						} else {
							creds, err := cfg.Credentials.Retrieve(ctx)
							if err != nil {
								fmt.Printf("Failed to retreive environmental AWS credentials: %s\n", err)
							} else {
								fmt.Printf("It looks like we found some environmental credentials:\n")
								fmt.Printf("  Access Key: %s\n", creds.AccessKeyID)
								fmt.Printf("  Secret Key: %s\n", strings.Repeat("*", len(creds.SecretAccessKey)))

								awsCreds = &creds
							}
						}

						if awsCreds != nil {
							useEnvToken := true
							if !autoConfig {
								fmt.Printf("Would you like to use these? [Y/n]: ")
								useEnvToken = readBool(true)
							}
							if useEnvToken {
								var useEnvEachTime bool

								if awsCreds.CanExpire {
									useEnvEachTime = true
									if !autoConfig {
										fmt.Printf("It appears these credentials will expire, instead of storing them\n")
										fmt.Printf(" should we use the environment each time? [Y/n]: ")
										useEnvEachTime = readBool(true)
									}
								} else {
									useEnvEachTime = false
									if !autoConfig {
										fmt.Printf("Should we use the environment each time? [y/N]: ")
										useEnvEachTime = readBool(false)
									}
								}

								if useEnvEachTime {
									fmt.Printf("Using environment from each run\n")
									awsUseEnv = true
									awsToken = "from-environment"
									awsSecret = "from-environment"
								} else {
									fmt.Printf("Storing credentials from environment\n")
									awsUseEnv = false
									awsToken = awsCreds.AccessKeyID
									awsSecret = awsCreds.SecretAccessKey
								}
							}
						}
					}
					if awsToken == "" {
						if !autoConfig {
							fmt.Printf("What AWS Access Key should we use? []: ")
							awsToken = readString("")
							if awsToken != "" {
								fmt.Printf("What AWS Secret Key should we use? []: ")
								awsSecret = readString("")

								if awsSecret == "" {
									fmt.Printf("You must specify the secret key for the access key.  Try again...\n")
									continue
								}
							}
						}
					}

					if !awsUseEnv && (awsToken == "" || awsSecret == "") {
						fmt.Printf("AWS support disabled.\n")
						break
					}

					if awsUseEnv {
						curConfig.AWS = &cbdcconfig.Config_AWS{
							FromEnvironment: true,
							AccessKey:       "",
							SecretKey:       "",
							Region:          "",
						}
					} else {
						curConfig.AWS = &cbdcconfig.Config_AWS{
							FromEnvironment: false,
							AccessKey:       awsToken,
							SecretKey:       awsSecret,
							Region:          "us-west-2",
						}
					}

					break
				}

				saveConfig()
			}
		}

		printCapellaConfig := func() {
			fmt.Printf("  Username: %s\n", curConfig.Capella.Username)
			fmt.Printf("  Password: %s\n", strings.Repeat("*", len(curConfig.Capella.Password)))
			fmt.Printf("  Organization ID: %s\n", curConfig.Capella.OrganizationID)
		}
		{
			flagCapellaUser, _ := cmd.Flags().GetString("capella-user")
			flagCapellaPass, _ := cmd.Flags().GetString("capella-pass")
			flagCapellaOid, _ := cmd.Flags().GetString("capella-oid")
			envCapellaUser := os.Getenv("CAPELLA_USER")
			envCapellaPass := os.Getenv("CAPELLA_PASS")
			envCapellaOid := os.Getenv("CAPELLA_OID")

			if flagCapellaUser != "" || flagCapellaPass != "" || flagCapellaOid != "" {
				curConfig.Capella = nil
			}
			if curConfig.Capella != nil {
				if autoConfig {
					// leave the original configuration
				} else {
					fmt.Printf("Found previous Capella configuration:\n")
					printCapellaConfig()
					fmt.Printf("Would you like to keep these settings? [Y/n]: ")
					if !readBool(true) {
						curConfig.Capella = nil
					}
				}
			}
			if curConfig.Capella == nil {
				for {
					curConfig.Capella = &cbdcconfig.Config_Capella{
						Username:       "",
						Password:       "",
						OrganizationID: "",
					}
					saveConfig()

					capellaUser := ""
					capellaPass := ""
					capellaOid := ""
					if capellaUser == "" {
						if flagCapellaUser != "" || flagCapellaPass != "" || flagCapellaOid != "" {
							if flagCapellaUser == "" || flagCapellaPass == "" || flagCapellaOid == "" {
								fmt.Printf("Must specify all three capella flags, skipping use of flags...")
							} else {
								fmt.Printf("Using capella config from flags\n")
								capellaUser = flagCapellaUser
								capellaPass = flagCapellaPass
								capellaOid = flagCapellaOid
							}
						}
					}
					if capellaUser == "" {
						if envCapellaUser != "" || envCapellaPass != "" || envCapellaOid != "" {
							if envCapellaUser == "" || envCapellaPass == "" || envCapellaOid == "" {
								fmt.Printf("Must specify all three capella env vars, skipping use of env...")
							} else {
								useEnvToken := true
								if !autoConfig {
									fmt.Printf("It looks like capella env vars were defined.\n")
									fmt.Printf("  CAPELLA_USER: %s\n", envCapellaUser)
									fmt.Printf("  CAPELLA_PASS: %s\n", strings.Repeat("*", len(envCapellaPass)))
									fmt.Printf("  CAPELLA_OID: %s\n", envCapellaOid)
									fmt.Printf("Would you like to use these? [Y/n]: ")
									useEnvToken = readBool(true)
								}
								if useEnvToken {
									fmt.Printf("Using capella credentials from env vars\n")
									capellaUser = envCapellaUser
									capellaPass = envCapellaPass
									capellaOid = envCapellaOid
								}
							}
						}
					}
					if capellaUser == "" {
						if !autoConfig {
							fmt.Printf("What Capella Username should we use? []: ")
							capellaUser = readString("")

							if capellaUser != "" {
								fmt.Printf("What Capella Password should we use? []: ")
								capellaPass = readString("")
								if capellaPass == "" {
									fmt.Printf("You must specify the password for that user.  Try again...\n")
									continue
								}

								fmt.Printf("What Capella Organization ID should we use? []: ")
								capellaOid = readString("")
								if capellaOid == "" {
									fmt.Printf("You must specify the organization id for that user.  Try again...\n")
									continue
								}
							}
						}
					}

					if capellaUser == "" || capellaPass == "" || capellaOid == "" {
						fmt.Printf("Capella support disabled.\n")
						break
					}

					curConfig.Capella = &cbdcconfig.Config_Capella{
						Username:       capellaUser,
						Password:       capellaPass,
						OrganizationID: capellaOid,
					}

					break
				}

				saveConfig()
			}
		}

		printBaseConfig := func() {
			fmt.Printf("  Default Cloud: %s\n", curConfig.DefaultCloud)
			fmt.Printf("  Default Deployer: %s\n", curConfig.DefaultDeployer)
		}
		{
			curConfig.DefaultCloud = "aws"
			curConfig.DefaultDeployer = "docker"
		}

		fmt.Printf("Initialization completed!\n")

		fmt.Printf("Using Docker configuration:\n")
		printDockerConfig()

		fmt.Printf("Using AWS configuration:\n")
		printAwsConfig()

		fmt.Printf("Using GitHub configuration:\n")
		printGitHubConfig()

		fmt.Printf("Using Capella configuration:\n")
		printCapellaConfig()

		fmt.Printf("Using Base configuration:\n")
		printBaseConfig()
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().Bool("auto", false, "Automatically setup without any interactivity")
	initCmd.Flags().String("docker-host", "", "Docker host address to use")
	initCmd.Flags().String("docker-network", "", "Docker network to use")
	initCmd.Flags().String("github-token", "", "GitHub token to use")
	initCmd.Flags().String("capella-user", "", "Capella user to use")
	initCmd.Flags().String("capella-pass", "", "Capella pass to use")
	initCmd.Flags().String("capella-oid", "", "Capella organization id to use")
	initCmd.Flags().String("aws-use-env", "", "Use the environment for AWS each run")
	initCmd.Flags().String("aws-access-key", "", "AWS access key to use")
	initCmd.Flags().String("aws-secret-key", "", "AWS secret key to use")
	initCmd.Flags().String("aws-region", "", "AWS default region to use")
}
