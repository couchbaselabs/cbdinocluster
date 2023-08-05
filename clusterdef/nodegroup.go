package clusterdef

type NodeGroup struct {
	Count int `yaml:"count"`

	Version  string    `yaml:"version"`
	Services []Service `json:"services"`

	*DockerNodeGroup
	*CloudNodeGroup
}

type DockerNodeGroup struct {
	Name string `yaml:"name"`
}

type CloudNodeGroup struct {
	InstanceType string `yaml:"instance-type"`
	DiskType     string `yaml:"disk-type"`
	DiskSize     int    `yaml:"disk-size"`
	DiskIops     int    `yaml:"disk-iops"`
}
