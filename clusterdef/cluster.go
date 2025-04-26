package clusterdef

import "time"

type Cluster struct {
	Deployer string `yaml:"deployer,omitempty"`

	Expiry  time.Duration `yaml:"expiry,omitempty"`
	Purpose string        `yaml:"purpose,omitempty"`

	Columnar   bool         `yaml:"columnar,omitempty"`
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

	Analytics          AnalyticsSettings `yaml:"analytics,omitempty"`
	EnableDNS          bool              `yaml:"dns,omitempty"`
	EnableLoadBalancer bool              `yaml:"load-balancer,omitempty"`
	UseDinoCerts       bool              `yaml:"use-dino-certs,omitempty"`
}

type AnalyticsSettings struct {
	BlobStorage AnalyticsBlobStorageSettings `yaml:"blob-storage,omitempty"`
}

type AnalyticsBlobStorageSettings struct {
	Region        string `yaml:"region,omitempty"`
	Prefix        string `yaml:"prefix,omitempty"`
	Bucket        string `yaml:"bucket,omitempty"`
	Scheme        string `yaml:"scheme,omitempty"`
	Endpoint      string `yaml:"endpoint,omitempty"`
	AnonymousAuth bool   `yaml:"anonymous-auth,omitempty"`
}

type CaoCluster struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`

	OperatorVersion string `yaml:"operator-version,omitempty"`
	GatewayVersion  string `yaml:"gateway-version,omitempty"`

	GatewayLogLevel     string `yaml:"gateway-log-level,omitempty"`
	GatewayOtlpEndpoint string `yaml:"gateway-otlp-endpoint,omitempty"`
}

type CloudCluster struct {
	CloudProvider string `yaml:"cloud-provider,omitempty"`
	Region        string `yaml:"region,omitempty"`
	Cidr          string `yaml:"cidr,omitempty"`
	FreeTier      bool   `yaml:"free-tier,omitempty"`
}
