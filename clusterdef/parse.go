package clusterdef

import (
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

func Parse(data []byte) (*Cluster, error) {
	var parsedDef *Cluster
	err := yaml.Unmarshal(data, &parsedDef)
	if err != nil {
		return nil, errors.Wrap(err, "yaml parsing failed")
	}

	if parsedDef.Docker._EnableLoadBalancer {
		parsedDef.Docker.PassiveLoadBalancer = true
	}

	return parsedDef, nil
}

func Stringify(cluster *Cluster) (string, error) {
	bytes, err := yaml.Marshal(cluster)
	if err != nil {
		return "", errors.Wrap(err, "yaml serialization failed")
	}

	return string(bytes), nil
}
