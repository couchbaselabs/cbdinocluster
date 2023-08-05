package cbdcconfig

import (
	"context"
	"os"
	"path"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

const Version = 2

type Config struct {
	Docker  *Config_Docker  `yaml:"docker"`
	GitHub  *Config_GitHub  `yaml:"github"`
	AWS     *Config_AWS     `yaml:"aws"`
	GCP     *Config_GCP     `yaml:"gcp"`
	Azure   *Config_Azure   `yaml:"azure"`
	Capella *Config_Capella `yaml:"capella"`

	Version         int    `yaml:"version"`
	DefaultCloud    string `yaml:"default-cloud"`
	DefaultDeployer string `yaml:"default-deployer"`
}

type Config_Docker struct {
	Host        string `yaml:"host"`
	Network     string `yaml:"network"`
	ForwardOnly bool   `yaml:"forward-only"`
}

type Config_GitHub struct {
	Token string `yaml:"token"`
	User  string `yaml:"user"`
}

type Config_AWS struct {
	FromEnvironment bool   `yaml:"from-env"`
	AccessKey       string `yaml:"access-key"`
	SecretKey       string `yaml:"secret-key"`
	DefaultRegion   string `yaml:"default-region"`
}

type Config_GCP struct {
	DefaultRegion string `yaml:"default-region"`
}

type Config_Azure struct {
	DefaultRegion string `yaml:"default-region"`
}

type Config_Capella struct {
	Username       string `yaml:"username"`
	Password       string `yaml:"password"`
	OrganizationID string `yaml:"organization-id"`
}

func DefaultConfigPath() (string, error) {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to find user home path")
	}

	configPath := path.Join(homePath, ".cbdinocluster")
	return configPath, nil
}

func Upgrade(config *Config) *Config {
	if config.Version < 2 {
		config.DefaultCloud = "aws"
		config.DefaultDeployer = "docker"
		if config.AWS != nil {
			config.AWS.DefaultRegion = "us-west-2"
		}
		config.Version = 2
	}

	return config
}

func Load(ctx context.Context) (*Config, error) {
	configPath, err := DefaultConfigPath()
	if err != nil {
		return nil, errors.Wrap(err, "failed to find default config path")
	}

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read config file")
	}

	var config *Config
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse config file")
	}

	if config.Version != Version {
		config = Upgrade(config)

		err := Save(ctx, config)
		if err != nil {
			return nil, errors.Wrap(err, "failed to save upgraded configuration")
		}
	}

	return config, nil
}

func Save(ctx context.Context, config *Config) error {
	configPath, err := DefaultConfigPath()
	if err != nil {
		return errors.Wrap(err, "failed to find default config path")
	}

	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return errors.Wrap(err, "failed to marshal config file")
	}

	err = os.WriteFile(configPath, configBytes, 0600)
	if err != nil {
		return errors.Wrap(err, "failed to write config file")
	}

	return nil
}
