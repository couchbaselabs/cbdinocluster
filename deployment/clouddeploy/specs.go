package clouddeploy

import (
	"slices"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/pkg/errors"
)

// azureUltraDisk is the only Azure disk type that accepts an explicit size and
// IOPS. The provisioned types (P6, P10 and so on) have both fixed.
const azureUltraDisk = "Ultra"

type cloudNodeDefaults struct {
	cpu      int
	ram      int
	diskType string
	diskSize int
	diskIops int
}

// All three providers default to 4 vCPU and 16 GB, which is what the instance
// types the v2 API path requested gave (m5.xlarge, n2-standard-4 and
// Standard_D4s_v5).
var nodeDefaultsByProvider = map[string]cloudNodeDefaults{
	capellav4.ProviderAws:   {cpu: 4, ram: 16, diskType: "gp3", diskSize: 50, diskIops: 3000},
	capellav4.ProviderGcp:   {cpu: 4, ram: 16, diskType: "pd-ssd", diskSize: 50},
	capellav4.ProviderAzure: {cpu: 4, ram: 16, diskType: "P6", diskSize: 64, diskIops: 240},
}

var defaultCloudServices = []clusterdef.Service{
	clusterdef.KvService,
	clusterdef.IndexService,
	clusterdef.QueryService,
	clusterdef.SearchService,
}

// buildServiceGroups translates the node groups of a cluster definition into the
// service groups that the Capella Management API v4 uses to describe a topology.
// The same shape is used to create a cluster and to rescale one.
func buildServiceGroups(
	cloudProvider string,
	nodeGrps []*clusterdef.NodeGroup,
) ([]capellav4.ServiceGroup, error) {
	defaults, ok := nodeDefaultsByProvider[cloudProvider]
	if !ok {
		return nil, errors.Errorf("invalid cloud provider `%s`", cloudProvider)
	}

	var groups []capellav4.ServiceGroup
	for _, nodeGroup := range nodeGrps {
		// The v4 API sizes nodes by cpu and ram and has no instance type field, so
		// a definition that names an instance type cannot be translated.
		if nodeGroup.Cloud.InstanceType != "" {
			return nil, errors.Errorf(
				"cloud instance-type `%s` is not supported for capella clusters, use cloud cpu and memory instead",
				nodeGroup.Cloud.InstanceType)
		}

		compute := capellav4.Compute{
			Cpu: defaults.cpu,
			Ram: defaults.ram,
		}
		if nodeGroup.Cloud.Cpu != 0 {
			compute.Cpu = nodeGroup.Cloud.Cpu
		}
		if nodeGroup.Cloud.Memory != 0 {
			compute.Ram = nodeGroup.Cloud.Memory
		}

		disk := capellav4.Disk{
			Type:    defaults.diskType,
			Storage: defaults.diskSize,
			Iops:    defaults.diskIops,
		}
		if nodeGroup.Cloud.DiskType != "" {
			disk.Type = nodeGroup.Cloud.DiskType
		}
		if nodeGroup.Cloud.DiskSize != 0 {
			disk.Storage = nodeGroup.Cloud.DiskSize
		}
		if nodeGroup.Cloud.DiskIops != 0 {
			disk.Iops = nodeGroup.Cloud.DiskIops
		}

		services := defaultCloudServices
		if len(nodeGroup.Services) > 0 {
			services = nodeGroup.Services
		}

		serviceNames, err := clusterdef.ServicesToCapellaServices(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate capella services list")
		}

		groups = append(groups, capellav4.ServiceGroup{
			Node: capellav4.Node{
				Compute: compute,
				Disk:    normalizeDisk(cloudProvider, disk),
			},
			NumOfNodes: nodeGroup.Count,
			Services:   serviceNames,
		})
	}

	return groups, nil
}

// normalizeDisk removes the disk fields that a provider does not accept. GCP has
// no IOPS setting, and Azure only reads the size and IOPS of an Ultra disk.
// Sending the extra fields would be rejected, and keeping them would make a
// cluster read back from Capella never compare equal to its own definition.
func normalizeDisk(cloudProvider string, disk capellav4.Disk) capellav4.Disk {
	switch cloudProvider {
	case capellav4.ProviderGcp:
		disk.Iops = 0
	case capellav4.ProviderAzure:
		if disk.Type != azureUltraDisk {
			disk.Storage = 0
			disk.Iops = 0
		}
	}
	return disk
}

// serviceGroupsEqual reports whether a cluster already has the wanted topology.
// It only compares what a definition can express, so fields that Capella manages
// by itself do not cause a needless rescale.
func serviceGroupsEqual(cloudProvider string, current, wanted []capellav4.ServiceGroup) bool {
	if len(current) != len(wanted) {
		return false
	}

	for i := range current {
		if current[i].NumOfNodes != wanted[i].NumOfNodes {
			return false
		}
		if current[i].Node.Compute != wanted[i].Node.Compute {
			return false
		}
		if normalizeDisk(cloudProvider, current[i].Node.Disk) != normalizeDisk(cloudProvider, wanted[i].Node.Disk) {
			return false
		}
		if !sameServices(current[i].Services, wanted[i].Services) {
			return false
		}
	}

	return true
}

func sameServices(services1, services2 []string) bool {
	if len(services1) != len(services2) {
		return false
	}

	sorted1 := slices.Clone(services1)
	sorted2 := slices.Clone(services2)
	slices.Sort(sorted1)
	slices.Sort(sorted2)

	return slices.Equal(sorted1, sorted2)
}
