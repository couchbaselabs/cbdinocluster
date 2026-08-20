package clouddeploy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

func getReleaseIdFromServerImage(serverImage string) (string, error) {
	// GCP images don't contain "." instead they have "-" and therefore cbdino is not able to read release ID for gcp images.
	// For example -
	// gcp image: "couchbase-cloud-server-7-6-5-5724-v1-0-53" has release ID "1.0.53"
	// aws image: "couchbase-cloud-server-7.6.5-5724-arm64-v1.0.53" has release ID "1.0.53"
	// azure image: "couchbase-cloud-server-7.6.5-v5720.0.53" has release ID "1.0.53"
	formattedServerImage := strings.ReplaceAll(serverImage, "-", ".")
	lastIndex := strings.LastIndex(formattedServerImage, ".")
	if lastIndex == -1 {
		// "." not found, handle error
		return "", errors.New(fmt.Sprintf("ServerImage is not in expected format"))
	}
	// The release ID is formed by combining the number after the last dot in the server image with "1.0.".
	releaseNumberStr := formattedServerImage[lastIndex+1:]

	// Validate if the release number is a valid integer
	releaseNumber, err := strconv.Atoi(releaseNumberStr)
	if err != nil {
		return "", errors.New("failed to parse release number")
	}

	releaseID := fmt.Sprintf("1.0.%d", releaseNumber)
	return releaseID, nil
}

func getReleaseIdFromColumnarServerImage(serverImage string) (string, error) {
	// Providers differ only in the separator, so normalising "-" to "." lets AWS and GCP share one rule:
	//
	//	aws: "enterprise-analytics-2.1.1-1469-arm64-v1.0.0" has release ID "2.1.1"
	//	gcp: "enterprise-analytics-2-1-1-1469-arm64-v1-0-0" has release ID "2.1.1"
	//	aws: "couchbase-columnar-1.1.2-1455-arm64-v1.0.12"  has release ID "1.1.2"
	//	gcp: "couchbase-columnar-1-1-2-1455-arm64-v1-0-12"  has release ID "1.1.2"
	for _, prefix := range []string{"enterprise-analytics-", "couchbase-columnar-"} {
		if strings.HasPrefix(serverImage, prefix) {
			version := strings.ReplaceAll(strings.TrimPrefix(serverImage, prefix), "-", ".")
			parts := strings.Split(version, ".")
			if len(parts) < 3 {
				return "", errors.Errorf("columnar server image %q is not in expected format", serverImage)
			}
			return strings.Join(parts[:3], "."), nil
		}
	}
	return "", errors.Errorf("columnar server image %q is not in expected format", serverImage)
}
