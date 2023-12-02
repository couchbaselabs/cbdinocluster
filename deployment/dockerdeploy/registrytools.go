package dockerdeploy

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/pkg/errors"
)

func doRegistryGet(ctx context.Context, url string, auth string, respData interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to list tags")
	}

	err = json.NewDecoder(resp.Body).Decode(respData)
	if err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	return nil
}
