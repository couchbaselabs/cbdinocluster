package azurecontrol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type LocalVmController struct {
	Logger *zap.Logger
}

type LocalVmInfo struct {
	Region string
	VmID   string
}

type azureImdsData_Compute struct {
	ResourceID string `json:"resourceId"`
	Location   string `json:"location"`
}

type azureImdsData struct {
	Compute azureImdsData_Compute `json:"compute"`
}

func (c *LocalVmController) Identify(ctx context.Context) (*LocalVmInfo, error) {
	client := http.Client{Transport: &http.Transport{Proxy: nil}}

	req, _ := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/instance", nil)
	req.Header.Add("Metadata", "True")

	q := req.URL.Query()
	q.Add("format", "json")
	q.Add("api-version", "2021-02-01")
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to query imds data")
	}

	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read imds meta-data")
	}

	var respData azureImdsData
	err = json.Unmarshal(respBytes, &respData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse imds meta-data")
	}

	c.Logger.Info("instance identity loaded", zap.Any("identity", respData))

	return &LocalVmInfo{
		Region: respData.Compute.Location,
		VmID:   respData.Compute.ResourceID,
	}, nil
}
