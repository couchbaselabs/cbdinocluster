package cbdcconfig

import (
	"context"
	"os"
	"path"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Docker  *Config_Docker  `yaml:"docker"`
	GitHub  *Config_GitHub  `yaml:"github"`
	AWS     *Config_AWS     `yaml:"aws"`
	Capella *Config_Capella `yaml:"capella"`
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
