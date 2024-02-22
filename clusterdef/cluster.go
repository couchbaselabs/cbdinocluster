package clusterdef

import "time"

type Cluster struct {
	Deployer string `yaml:"deployer,omitempty"`

	Expiry  time.Duration `yaml:"expiry,omitempty"`
	Purpose string        `yaml:"purpose,omitempty"`

	NodeGroups []*NodeGroup `yaml:"nodes,omitempty"`

	Docker DockerCluster `yaml:"docker,omitempty"`
	Cao    CaoCluster    `yaml:"cao,omitempty"`
	Cloud  CloudCluster  `yaml:"cloud,omitempty"`
}

type DockerCluster struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`

	KvMemoryMB       int `yaml:"kv-memory,omitempty"`
	IndexMemoryMB    int `yaml:"index-memory,omitempty"`
	FtsMemoryMB      int `yaml:"fts-memory,omitempty"`
	CbasMemoryMB     int `yaml:"cbas-memory,omitempty"`
	EventingMemoryMB int `yaml:"eventing-memory,omitempty"`
}

type CaoCluster struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`

	UseIngress      bool   `yaml:"use-ingress,omitempty"`
	OperatorVersion string `yaml:"operator-version,omitempty"`
	GatewayVersion  string `yaml:"gateway-version,omitempty"`
}

type CloudCluster struct {
	CloudProvider string `yaml:"cloud-provider,omitempty"`
	Region        string `yaml:"region,omitempty"`
	Cidr          string `yaml:"cidr,omitempty"`
}
