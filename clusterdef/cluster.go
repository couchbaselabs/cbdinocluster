package clusterdef

import "time"

type Cluster struct {
	Name    string        `yaml:"name"`
	Expiry  time.Duration `yaml:"expiry"`
	Purpose string        `yaml:"purpose"`

	NodeGroups []*NodeGroup `yaml:"nodes"`

	*DockerCluster
	*CloudCluster
}

type DockerCluster struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`

	KvMemoryMB       int `yaml:"kv-memory"`
	IndexMemoryMB    int `yaml:"index-memory"`
	FtsMemoryMB      int `yaml:"fts-memory"`
	CbasMemoryMB     int `yaml:"cbas-memory"`
	EventingMemoryMB int `yaml:"eventing-memory"`
}

type CloudCluster struct {
	CloudProvider string `yaml:"cloud-provider"`
	Region        string `yaml:"region"`
	Cidr          string `yaml:"cidr"`
}
