package caocontrol

import (
	"github.com/couchbaselabs/cbdinocluster/clusterdef"
)

type cbcConfig struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}
type Metadata struct {
	Name string `yaml:"name"`
}
type Rbac struct {
	Managed bool `yaml:"managed"`
}
type Security struct {
	AdminSecret string `yaml:"adminSecret"`
	Rbac        Rbac   `yaml:"rbac"`
}
type Cluster struct {
	ClusterName string `yaml:"clusterName"`
}
type Buckets struct {
	Managed bool `yaml:"managed"`
}
type ImagePullSecrets struct {
	Name string `yaml:"name"`
}
type PodSpec struct {
	ImagePullSecrets []ImagePullSecrets `yaml:"imagePullSecrets,omitempty"`
}
type Pod struct {
	Spec PodSpec `yaml:"spec,omitempty"`
}
type Servers struct {
	Size     int      `yaml:"size"`
	Name     string   `yaml:"name"`
	Services []string `yaml:"services"`
	Pod      Pod      `yaml:"pod,omitempty"`
}
type TLS struct {
	ServerSecretName string `yaml:"serverSecretName,omitempty"`
}
type CloudNativeGateway struct {
	Image string `yaml:"image"`
	TLS   TLS    `yaml:"tls,omitempty"`
}
type Networking struct {
	CloudNativeGateway CloudNativeGateway `yaml:"cloudNativeGateway"`
}
type Spec struct {
	Image      string     `yaml:"image"`
	Security   Security   `yaml:"security"`
	Cluster    Cluster    `yaml:"cluster"`
	Buckets    Buckets    `yaml:"buckets"`
	Servers    []Servers  `yaml:"servers"`
	Networking Networking `yaml:"networking"`
}

func servicesFromList(in []clusterdef.Service) []string {
	out := []string{}

	for _, svc := range in {
		switch svc {
		case "kv", "data":
			out = append(out, "data")
		case "index":
			out = append(out, "index")
		case "n1ql", "query":
			out = append(out, "query")
		case "fts", "search":
			out = append(out, "search")
		case "eventing":
			out = append(out, "eventing")
		case "cbas", "analytics":
			out = append(out, "analytics")
		}
	}

	return out
}

func (c *Controller) NewCbcConfig(name string, nodeGroup *clusterdef.NodeGroup, manageBucket bool) *cbcConfig {
	cbcc := cbcConfig{
		APIVersion: "couchbase.com/v2",
		Kind: "CouchbaseCluster",
		Metadata: Metadata{
			Name: name,
		},
		Spec: Spec{
			Image: nodeGroup.ServerImage,
			Buckets: Buckets{
				Managed: manageBucket, // must be false, if bucket management via client
			},
			Servers: []Servers{
				{
					Size: nodeGroup.Count,
					Name: "cbdc_services",
					Services: servicesFromList(nodeGroup.Services),
				},
			},
		},
	}

	cbcc.Spec.Cluster.ClusterName = "fancy" + "-" + name

	if nodeGroup.Cao.CNGImage != "" {
		cbcc.WithCNG(nodeGroup.Cao.CNGImage )
	}
	
	return &cbcc
}

func (cbcc *cbcConfig) WithCNG(image string) {
	if image != "" {
		cbcc.Spec.Networking.CloudNativeGateway.Image = image
	}
}

func (cbcc *cbcConfig) WithCNGTLS(tlsSecretName string) {
	if tlsSecretName != "" && cbcc.Spec.Networking.CloudNativeGateway.Image != "" {
		cbcc.Spec.Networking.CloudNativeGateway.TLS.ServerSecretName = tlsSecretName
	}
}

func (cbcc *cbcConfig) WithAdminSecret(adminSecretName string) {
	cbcc.Spec.Security = Security{
		AdminSecret: adminSecretName,
		Rbac: Rbac{
			Managed: true,
		},
	}
}

func (cbcc *cbcConfig) IsCNGTLSEnabled() bool {
	return cbcc.Spec.Networking.CloudNativeGateway.TLS.ServerSecretName != ""
}