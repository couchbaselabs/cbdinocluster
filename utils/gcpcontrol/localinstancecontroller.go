package gcpcontrol

import (
	"context"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"io"
	"net/http"
)

type LocalInstanceController struct {
	Logger *zap.Logger
}

type LocalInstanceInfo struct {
	Zone       string
	InstanceID string
	ProjectID  string
}

// FetchGCPMetadata fetches a value from the GCP metadata server.
func FetchGCPMetadata(ctx context.Context, path string) (string, error) {
	client := http.Client{Transport: &http.Transport{Proxy: nil}}
	url := "http://metadata.google.internal/computeMetadata/v1/" + path

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Add("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to fetch metadata")
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", errors.Wrap(err, "failed to read metadata response")
	}
	return string(body), errors.Wrap(err, "failed to read metadata response")
}

func (c *LocalInstanceController) Identify(ctx context.Context) (*LocalInstanceInfo, error) {
	zone, err := FetchGCPMetadata(ctx, "instance/zone")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get zone")
	}

	instanceID, err := FetchGCPMetadata(ctx, "instance/id")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get instance ID")
	}

	projectID, err := FetchGCPMetadata(ctx, "project/project-id")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get project ID")
	}

	c.Logger.Info("instance identity loaded",
		zap.String("zone", zone),
		zap.String("instanceID", instanceID),
		zap.String("projectID", projectID))

	return &LocalInstanceInfo{
		Zone:       zone,
		InstanceID: instanceID,
		ProjectID:  projectID,
	}, nil
}
