package caocontrol

import (
	"context"
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
	"github.com/pkg/errors"
)

func parseSimpleVersion(ctx context.Context, version string) (string, int, error) {
	ver, err := versionident.Identify(ctx, version)
	if err != nil {
		return "", 0, err
	}

	if ver.CommunityEdition || ver.Serverless {
		return "", 0, errors.New("invalid version format")
	}

	return ver.Version, ver.BuildNo, nil
}

func GetAdmissionControllerImage(ctx context.Context, version string) (string, error) {
	if version[0] == '@' {
		return version[1:], nil
	}

	version, buildNo, err := parseSimpleVersion(ctx, version)
	if err != nil {
		return "", err
	}

	image := ""
	if buildNo == 0 {
		image = fmt.Sprintf("couchbase/admission-controller:%s", version)
	} else {
		image = fmt.Sprintf("ghcr.io/cb-vanilla/admission-controller:%s-%d", version, buildNo)
	}

	return image, nil
}

func GetOperatorImage(ctx context.Context, version string, needRhcc bool) (string, error) {
	if version[0] == '@' {
		return version[1:], nil
	}

	version, buildNo, err := parseSimpleVersion(ctx, version)
	if err != nil {
		return "", err
	}

	image := ""
	if buildNo == 0 {
		image = fmt.Sprintf("couchbase/operator:%s", version)
	} else {
		if !needRhcc {
			image = fmt.Sprintf("ghcr.io/cb-vanilla/operator:%s-%d", version, buildNo)
		} else {
			image = fmt.Sprintf("ghcr.io/cb-rhcc/operator:%s-%d", version, buildNo)
		}
	}

	return image, nil
}

func GetGatewayImage(ctx context.Context, version string, needRhcc bool) (string, error) {
	if version[0] == '@' {
		return version[1:], nil
	}

	version, buildNo, err := parseSimpleVersion(ctx, version)
	if err != nil {
		return "", err
	}

	image := ""
	if buildNo == 0 {
		image = fmt.Sprintf("couchbase/cloud-native-gateway:%s", version)
	} else {
		if !needRhcc {
			image = fmt.Sprintf("ghcr.io/cb-vanilla/cloud-native-gateway:%s-%d", version, buildNo)
		} else {
			image = fmt.Sprintf("ghcr.io/cb-rhcc/cloud-native-gateway:%s-%d", version, buildNo)
		}
	}

	return image, nil
}

func GetServerImage(ctx context.Context, version string, needRhcc bool) (string, error) {
	if version[0] == '@' {
		return version[1:], nil
	}

	ver, err := versionident.Identify(ctx, version)
	if err != nil {
		return "", err
	}

	if ver.Serverless {
		return "", errors.New("cao does not support serverless images")
	}

	image := ""
	if ver.BuildNo == 0 {
		if !ver.CommunityEdition {
			image = fmt.Sprintf("couchbase/server:%s", ver.Version)
		} else {
			image = fmt.Sprintf("couchbase/server:community-%s", ver.Version)
		}
	} else {
		if !needRhcc {
			if !ver.CommunityEdition {
				image = fmt.Sprintf("ghcr.io/cb-vanilla/server:%s-%d", ver.Version, ver.BuildNo)
			} else {
				image = fmt.Sprintf("ghcr.io/cb-vanilla/server:community-%s-%d", ver.Version, ver.BuildNo)
			}
		} else {
			if !ver.CommunityEdition {
				image = fmt.Sprintf("ghcr.io/cb-rhcc/server:%s-%d", ver.Version, ver.BuildNo)
			} else {
				image = fmt.Sprintf("ghcr.io/cb-rhcc/server:community-%s-%d", ver.Version, ver.BuildNo)
			}
		}
	}

	return image, nil
}
