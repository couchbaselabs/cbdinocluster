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

	return parsedDef, nil
}
