package clouddeploy

import (
	"testing"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildServiceGroupsProviderDefaults(t *testing.T) {
	tests := []struct {
		provider string
		expected capellav4.ServiceGroup
	}{
		{
			provider: capellav4.ProviderAws,
			expected: capellav4.ServiceGroup{
				Node: capellav4.Node{
					Compute: capellav4.Compute{Cpu: 4, Ram: 16},
					Disk:    capellav4.Disk{Type: "gp3", Storage: 50, Iops: 3000},
				},
				NumOfNodes: 3,
				Services:   []string{"data", "index", "query", "search"},
			},
		},
		{
			provider: capellav4.ProviderGcp,
			expected: capellav4.ServiceGroup{
				Node: capellav4.Node{
					Compute: capellav4.Compute{Cpu: 4, Ram: 16},
					// GCP has no IOPS setting.
					Disk: capellav4.Disk{Type: "pd-ssd", Storage: 50},
				},
				NumOfNodes: 3,
				Services:   []string{"data", "index", "query", "search"},
			},
		},
		{
			provider: capellav4.ProviderAzure,
			expected: capellav4.ServiceGroup{
				Node: capellav4.Node{
					Compute: capellav4.Compute{Cpu: 4, Ram: 16},
					// A provisioned Azure disk has a fixed size and IOPS.
					Disk: capellav4.Disk{Type: "P6"},
				},
				NumOfNodes: 3,
				Services:   []string{"data", "index", "query", "search"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			groups, err := buildServiceGroups(test.provider, []*clusterdef.NodeGroup{
				{Count: 3},
			})
			require.NoError(t, err)
			require.Len(t, groups, 1)
			assert.Equal(t, test.expected, groups[0])
		})
	}
}

func TestBuildServiceGroupsOverrides(t *testing.T) {
	groups, err := buildServiceGroups(capellav4.ProviderAws, []*clusterdef.NodeGroup{
		{
			Count:    2,
			Services: []clusterdef.Service{clusterdef.KvService},
			Cloud: clusterdef.CloudNodeGroup{
				Cpu:      8,
				Memory:   32,
				DiskType: "io2",
				DiskSize: 100,
				DiskIops: 4000,
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, groups, 1)

	assert.Equal(t, capellav4.ServiceGroup{
		Node: capellav4.Node{
			Compute: capellav4.Compute{Cpu: 8, Ram: 32},
			Disk:    capellav4.Disk{Type: "io2", Storage: 100, Iops: 4000},
		},
		NumOfNodes: 2,
		Services:   []string{"data"},
	}, groups[0])
}

// An Ultra disk is the one Azure type that reads an explicit size and IOPS.
func TestBuildServiceGroupsAzureUltraDisk(t *testing.T) {
	groups, err := buildServiceGroups(capellav4.ProviderAzure, []*clusterdef.NodeGroup{
		{
			Count: 3,
			Cloud: clusterdef.CloudNodeGroup{
				DiskType: azureUltraDisk,
				DiskSize: 128,
				DiskIops: 3000,
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, groups, 1)

	assert.Equal(t, capellav4.Disk{Type: "Ultra", Storage: 128, Iops: 3000}, groups[0].Node.Disk)
}

func TestBuildServiceGroupsMultipleGroups(t *testing.T) {
	groups, err := buildServiceGroups(capellav4.ProviderAws, []*clusterdef.NodeGroup{
		{Count: 3, Services: []clusterdef.Service{clusterdef.KvService}},
		{Count: 2, Services: []clusterdef.Service{clusterdef.AnalyticsService}},
	})
	require.NoError(t, err)
	require.Len(t, groups, 2)

	assert.Equal(t, 3, groups[0].NumOfNodes)
	assert.Equal(t, []string{"data"}, groups[0].Services)
	assert.Equal(t, 2, groups[1].NumOfNodes)
	assert.Equal(t, []string{"analytics"}, groups[1].Services)
}

// The v4 API sizes nodes by cpu and ram, so an instance type cannot be honoured.
// The error must say what to use instead.
func TestBuildServiceGroupsRejectsInstanceType(t *testing.T) {
	_, err := buildServiceGroups(capellav4.ProviderAws, []*clusterdef.NodeGroup{
		{
			Count: 3,
			Cloud: clusterdef.CloudNodeGroup{InstanceType: "m5.24xlarge"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "m5.24xlarge")
	assert.Contains(t, err.Error(), "cpu and memory")
}

func TestBuildServiceGroupsRejectsUnknownProvider(t *testing.T) {
	_, err := buildServiceGroups("oracle", []*clusterdef.NodeGroup{{Count: 3}})
	require.Error(t, err)
}

func TestBuildServiceGroupsRejectsUnsupportedService(t *testing.T) {
	_, err := buildServiceGroups(capellav4.ProviderAws, []*clusterdef.NodeGroup{
		{Count: 3, Services: []clusterdef.Service{clusterdef.BackupService}},
	})
	require.Error(t, err)
}

func TestServiceGroupsEqual(t *testing.T) {
	base := capellav4.ServiceGroup{
		Node: capellav4.Node{
			Compute: capellav4.Compute{Cpu: 4, Ram: 16},
			Disk:    capellav4.Disk{Type: "gp3", Storage: 50, Iops: 3000},
		},
		NumOfNodes: 3,
		Services:   []string{"data", "index", "query", "search"},
	}

	reordered := base
	reordered.Services = []string{"search", "query", "index", "data"}
	assert.True(t, serviceGroupsEqual(capellav4.ProviderAws, []capellav4.ServiceGroup{base}, []capellav4.ServiceGroup{reordered}),
		"service order must not count as a change")

	scaled := base
	scaled.NumOfNodes = 4
	assert.False(t, serviceGroupsEqual(capellav4.ProviderAws, []capellav4.ServiceGroup{base}, []capellav4.ServiceGroup{scaled}))

	resized := base
	resized.Node.Compute = capellav4.Compute{Cpu: 8, Ram: 32}
	assert.False(t, serviceGroupsEqual(capellav4.ProviderAws, []capellav4.ServiceGroup{base}, []capellav4.ServiceGroup{resized}))

	fewerServices := base
	fewerServices.Services = []string{"data"}
	assert.False(t, serviceGroupsEqual(capellav4.ProviderAws, []capellav4.ServiceGroup{base}, []capellav4.ServiceGroup{fewerServices}))

	assert.False(t, serviceGroupsEqual(capellav4.ProviderAws, []capellav4.ServiceGroup{base}, nil))
}

// Capella reports a size and IOPS for provisioned Azure disks even though a
// definition cannot set them. Reporting that as a change would rescale the
// cluster on every modify.
func TestServiceGroupsEqualIgnoresAzureReportedDisk(t *testing.T) {
	wanted, err := buildServiceGroups(capellav4.ProviderAzure, []*clusterdef.NodeGroup{{Count: 3}})
	require.NoError(t, err)

	current := wanted[0]
	current.Node.Disk.Storage = 64
	current.Node.Disk.Iops = 240

	assert.True(t, serviceGroupsEqual(capellav4.ProviderAzure, []capellav4.ServiceGroup{current}, wanted))
}
