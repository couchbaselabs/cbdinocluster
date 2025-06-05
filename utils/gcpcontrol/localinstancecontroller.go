package gcpcontrol

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type LocalInstanceController struct {
	Logger *zap.Logger
}

type LocalInstanceInfo struct {
	Region     string
	InstanceID string
}

func (c *LocalInstanceController) Identify(ctx context.Context) (*LocalInstanceInfo, error) {
	client := &http.Client{}
	baseURL := "http://metadata.google.internal/computeMetadata/v1/instance"

	// Helper function to get metadata
	getMetadata := func(path string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/%s", baseURL, path), nil)
		if err != nil {
			return "", errors.Wrap(err, "failed to create request")
		}
		req.Header.Set("Metadata-Flavor", "Google")

		resp, err := client.Do(req)
		if err != nil {
			return "", errors.New("must be running within a GCP instance")
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", errors.Wrap(err, "failed to read response body")
		}
		return strings.TrimSpace(string(body)), nil
	}

	zone, err := getMetadata("zone")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get zone")
	}

	// Extract region from zone (format: projects/PROJECT_NUMBER/zones/ZONE)
	parts := strings.Split(zone, "/")
	if len(parts) < 4 {
		return nil, errors.New("invalid zone format")
	}
	regionZone := parts[3]
	region := strings.Split(regionZone, "-")[0] + "-" + strings.Split(regionZone, "-")[1] // us-west1

	instanceID, err := getMetadata("id")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get instance ID")
	}

	c.Logger.Info("instance identity loaded",
		zap.String("region", region),
		zap.String("instanceID", instanceID))

	return &LocalInstanceInfo{
		Region:     region,
		InstanceID: instanceID,
	}, nil
}
