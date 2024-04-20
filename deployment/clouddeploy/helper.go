package clouddeploy

import (
	"fmt"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/pkg/errors"
	"sort"
	"strconv"
	"strings"
)

func compareUpdateClusterSpecs(spec1, spec2 capellacontrol.UpdateClusterSpecsRequest_Spec) int {
	if spec1.Compute.Type != spec2.Compute.Type || spec1.Count != spec2.Count || spec1.Disk.Type != spec2.Disk.Type ||
		spec1.Disk.SizeInGb != spec2.Disk.SizeInGb || spec1.Disk.Iops != spec2.Disk.Iops || spec1.DiskAutoScaling.Enabled != spec2.DiskAutoScaling.Enabled {
		return 0
	}

	if len(spec2.Services) != len(spec1.Services) {
		return 0
	}
	var specList1 []string
	var specList2 []string
	for i := range spec1.Services {

		specList1 = append(specList1, spec1.Services[i].Type)
		specList2 = append(specList2, spec2.Services[i].Type)
	}

	sort.Strings(specList1)
	sort.Strings(specList2)

	for i := range specList1 {
		if specList1[i] != specList2[i] {
			return 0
		}
	}

	return 1
}
func isNotEqual(spec1, spec2 capellacontrol.UpdateClusterSpecsRequest_Spec) bool {
	// compare specs and return true if not equal otherwise false
	if compareUpdateClusterSpecs(spec1, spec2) == 0 {
		return true
	} else {
		return false
	}
}

func convertClusterServicesToSpecServices(services1 []capellacontrol.ClusterInfo_Service_Service) []capellacontrol.UpdateClusterSpecsRequest_Spec_Service {

	// Initialize the converted services slice
	convertedServices1 := make([]capellacontrol.UpdateClusterSpecsRequest_Spec_Service, len(services1))
	for i := range services1 {
		convertedServices1[i] = capellacontrol.UpdateClusterSpecsRequest_Spec_Service{
			Type: services1[i].Type,
		}
	}

	return convertedServices1
}

func isServiceEqual(services1 []capellacontrol.ClusterInfo_Service, services2 []capellacontrol.UpdateClusterSpecsRequest_Spec) bool {
	if len(services1) != len(services2) {
		return false
	}
	for i := range services1 {
		if i >= len(services2) {
			return false
		}
		convertedService1 := capellacontrol.UpdateClusterSpecsRequest_Spec{
			Compute: capellacontrol.UpdateClusterSpecsRequest_Spec_Compute{
				Type: services1[i].Compute.Type,
			},
			Count: services1[i].Count,
			Disk: capellacontrol.UpdateClusterSpecsRequest_Spec_Disk{
				Type:     services1[i].Disk.Type,
				SizeInGb: services1[i].Disk.SizeInGb,
				Iops:     services1[i].Disk.Iops,
			},
			DiskAutoScaling: capellacontrol.UpdateClusterSpecsRequest_Spec_DiskScaling{
				Enabled: services1[i].DiskAutoScaling.Enabled,
			},
			Services: convertClusterServicesToSpecServices(services1[i].Services),
		}
		if isNotEqual(convertedService1, services2[i]) {
			return false
		}
	}

	return true
}

func getReleaseIdFromServerImage(serverImage string) (string, error) {
	lastIndex := strings.LastIndex(serverImage, ".")
	if lastIndex == -1 {
		// "." not found, handle error
		return "", errors.New(fmt.Sprintf("ServerImage is not in expected format"))
	}
	// The release ID is formed by combining the number after the last dot in the server image with "1.0.".
	releaseNumberStr := serverImage[lastIndex+1:]

	// Validate if the release number is a valid integer
	releaseNumber, err := strconv.Atoi(releaseNumberStr)
	if err != nil {
		return "", errors.New("failed to parse release number")
	}

	releaseID := fmt.Sprintf("1.0.%d", releaseNumber)
	return releaseID, nil
}
