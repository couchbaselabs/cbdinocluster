package clouddeploy

import (
	"slices"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/pkg/errors"
)

// Ultra is the only Azure disk type that accepts an explicit size and IOPS.
const azureUltraDisk = "Ultra"

type cloudNodeDefaults struct {
	cpu      int
	ram      int
	diskType string
	diskSize int
	diskIops int
}

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
		// The v4 API sizes nodes by cpu and ram. It has no instance type field.
		// A def with server-image deploys through the legacy path, which reads
		// instance-type, so the field is tolerated there and ignored here.
		if nodeGroup.Cloud.InstanceType != "" && nodeGroup.Cloud.ServerImage == "" {
			return nil, errors.Errorf(
				"cloud instance-type `%s` is only supported together with cloud server-image, use cloud cpu and memory instead",
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

// GCP has no IOPS setting. Azure reads the size and IOPS of an Ultra disk only.
// Capella rejects the extra fields.
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

// Only the fields that a definition can express are compared. Capella manages
// the others by itself. The v4 API does not guarantee the order of the
// reported service groups, so the groups are matched as a multiset.
func serviceGroupsEqual(cloudProvider string, current, wanted []capellav4.ServiceGroup) bool {
	if len(current) != len(wanted) {
		return false
	}

	matched := make([]bool, len(current))
	for _, wantedGroup := range wanted {
		found := false
		for i, currentGroup := range current {
			if matched[i] {
				continue
			}
			if serviceGroupMatches(cloudProvider, currentGroup, wantedGroup) {
				matched[i] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func serviceGroupMatches(cloudProvider string, current, wanted capellav4.ServiceGroup) bool {
	if current.NumOfNodes != wanted.NumOfNodes {
		return false
	}
	if current.Node.Compute != wanted.Node.Compute {
		return false
	}
	if comparableDisk(cloudProvider, current.Node.Disk) != comparableDisk(cloudProvider, wanted.Node.Disk) {
		return false
	}
	return sameServices(current.Services, wanted.Services)
}

// A definition cannot express autoExpansion, and Capella reports it back on
// Azure, so it must not count as a change.
func comparableDisk(cloudProvider string, disk capellav4.Disk) capellav4.Disk {
	disk = normalizeDisk(cloudProvider, disk)
	disk.AutoExpansion = false
	return disk
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
