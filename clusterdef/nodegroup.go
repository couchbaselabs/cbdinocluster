package clusterdef

type NodeGroup struct {
	// Count specifies the number of nodes of this type to create.
	Count int `yaml:"count,omitempty"`

	// ForceNew forces new nodes to be provisioned instead of reusing
	// any existing nodes when doing modifications.
	ForceNew bool `yaml:"force-new,omitempty"`

	Version  string    `yaml:"version,omitempty"`
	Services []Service `yaml:"services,omitempty"`

	Docker DockerNodeGroup `yaml:"docker,omitempty"`
	Cloud  CloudNodeGroup  `yaml:"cloud,omitempty"`
}

type DockerNodeGroup struct {
}

type CloudNodeGroup struct {
	InstanceType string `yaml:"instance-type,omitempty"`
	DiskType     string `yaml:"disk-type,omitempty"`
	DiskSize     int    `yaml:"disk-size,omitempty"`
	DiskIops     int    `yaml:"disk-iops,omitempty"`
}
