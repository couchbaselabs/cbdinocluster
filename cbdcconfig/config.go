package cbdcconfig

import (
	"context"
	"os"
	"path"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

const Version = 6

type StringBool string

func (b StringBool) IsSet() bool {
	return b != ""
}

func (b StringBool) Value() bool {
	return b == "true"
}

func (b StringBool) ValueOr(defaultValue bool) bool {
	if b == "" {
		return defaultValue
	}
	return b.Value()
}

func (b *StringBool) Set(value bool) {
	if value {
		*b = "true"
	} else {
		*b = "false"
	}
}

func (b *StringBool) Clear() {
	*b = ""
}

type Config struct {
	Version int `yaml:"version"`

	Docker  Config_Docker  `yaml:"docker"`
	GitHub  Config_GitHub  `yaml:"github"`
	AWS     Config_AWS     `yaml:"aws"`
	GCP     Config_GCP     `yaml:"gcp"`
	Azure   Config_Azure   `yaml:"azure"`
	Capella Config_Capella `yaml:"capella"`
	Cao 	Config_Cao 	   `yaml:"cao"`

	DefaultDeployer string        `yaml:"default-deployer"`
	DefaultExpiry   time.Duration `yaml:"default-expiry"`

	_DefaultCloud string `yaml:"default-cloud"`
}

type Config_Docker struct {
	Enabled     StringBool `yaml:"enabled"`
	Host        string     `yaml:"host"`
	Network     string     `yaml:"network"`
	ForwardOnly StringBool `yaml:"forward-only"`
}

type Config_GitHub struct {
	Enabled StringBool `yaml:"enabled"`
	Token   string     `yaml:"token"`
	User    string     `yaml:"user"`
}

type Config_AWS struct {
	Enabled StringBool `yaml:"enabled"`
	Region  string     `yaml:"region"`

	_DefaultRegion string `yaml:"default-region"`
}

type Config_GCP struct {
	Enabled StringBool `yaml:"enabled"`
	Region  string     `yaml:"region"`

	_DefaultRegion string `yaml:"default-region"`
}

type Config_Azure struct {
	Enabled StringBool `yaml:"enabled"`

	Region string `yaml:"region"`
	SubID  string `yaml:"sub-id"`
	RGName string `yaml:"rg-name"`

	_DefaultRegion string `yaml:"default-region"`
}

type Config_Capella struct {
	Enabled        StringBool `yaml:"enabled"`
	Endpoint       string     `yaml:"endpoint"`
	Username       string     `yaml:"username"`
	Password       string     `yaml:"password"`
	OrganizationID string     `yaml:"organization-id"`

	DefaultCloud       string `yaml:"default-cloud"`
	DefaultAwsRegion   string `yaml:"default-aws-region"`
	DefaultAzureRegion string `yaml:"default-azure-region"`
	DefaultGcpRegion   string `yaml:"default-gcp-region"`
}

type Config_Cao struct {
	Enabled 			StringBool `yaml:"enabled"`
	KubeConfigPath		string	`yaml:"kubeconfig"`

	// for pulling ghcr images
	NeedGhcrAccess		StringBool `yaml:"needGhcrAccess"`
	GhcrToken   		string     `yaml:"ghcrToken"`
	GhcrUser    		string     `yaml:"ghcrUser"`

	OperatorVersion     string `yaml:"operatorVersion"`
	OperatorNamespace   string `yaml:"operatorNamespace"`
	AdmissionVersion    string `yaml:"admissionVersion"`
	AdmissionNamespace  string `yaml:"admissionNamespace"`
	CrdPath				string `yaml:"crdPath"`
	CaoBinPath			string `yaml:"caoBinPath"`
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
		config._DefaultCloud = "aws"
		config.DefaultDeployer = "docker"
		config.AWS._DefaultRegion = DEFAULT_AWS_REGION
		config.Azure._DefaultRegion = DEFAULT_AZURE_REGION
		config.GCP._DefaultRegion = DEFAULT_GCP_REGION
		config.Version = 2
	}

	if config.Version < 3 {
		config.AWS.Region = config.AWS._DefaultRegion
		config.GCP.Region = config.GCP._DefaultRegion
		config.Azure.Region = config.Azure._DefaultRegion
		config.Version = 3
	}

	if config.Version < 4 {
		config.Capella.DefaultCloud = config._DefaultCloud
		config.Version = 4
	}

	if config.Version < 5 {
		config.Capella.Endpoint = DEFAULT_CAPELLA_ENDPOINT
		config.Version = 5
	}

	if config.Version < 6 {
		config.DefaultExpiry = 0
		config.Version = 6
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
