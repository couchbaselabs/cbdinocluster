package clusterdef

type NodeGroup struct {
	// Count specifies the number of nodes of this type to create.
	Count int `yaml:"count,omitempty"`

	// ServerGroup name to assign the nodes on the cluster if applicable.
	ServerGroup string `yaml:"server-group,omitempty"`

	// ForceNew forces new nodes to be provisioned instead of reusing
	// any existing nodes when doing modifications.
	ForceNew bool `yaml:"force-new,omitempty"`

	Version  string    `yaml:"version,omitempty"`
	Services []Service `yaml:"services,omitempty"`

	Docker DockerNodeGroup `yaml:"docker,omitempty"`
	Cloud  CloudNodeGroup  `yaml:"cloud,omitempty"`
}

type DockerNodeGroup struct {
	EnvVars map[string]string `yaml:"env,omitempty"`
}

type CloudNodeGroup struct {
	InstanceType string `yaml:"instance-type,omitempty"`
	Cpu          int    `yaml:"cpu,omitempty"`
	Memory       int    `yaml:"memory,omitempty"`
	ServerImage  string `yaml:"server-image,omitempty"`
	DiskType     string `yaml:"disk-type,omitempty"`
	DiskSize     int    `yaml:"disk-size,omitempty"`
	DiskIops     int    `yaml:"disk-iops,omitempty"`
}
