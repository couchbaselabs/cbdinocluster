package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/peterhellberg/link"
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

func doRegistryListTagsGet(ctx context.Context, url string, auth string, respData interface{}) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to create request")
	}

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to list tags")
	}

	err = json.NewDecoder(resp.Body).Decode(respData)
	if err != nil {
		return "", errors.Wrap(err, "failed to decode response")
	}

	linkHdr := resp.Header.Get("Link")
	links := link.Parse(linkHdr)

	nextLink := ""
	for _, l := range links {
		if l.Rel == "next" {
			nextLink = l.URI
			break
		}
	}

	return nextLink, nil
}

func doRegistryListTags(ctx context.Context, url string, repo string, image string, auth string) ([]string, error) {
	var respData struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}

	nextPath := fmt.Sprintf("/v2/%s/%s/tags/list?n=1000", repo, image)
	var allTags []string
	for nextPath != "" {
		reqNextPath, err := doRegistryListTagsGet(ctx, url+nextPath, auth, &respData)
		if err != nil {
			return nil, errors.Wrap(err, "failed to search images")
		}

		allTags = append(allTags, respData.Tags...)

		nextPath = reqNextPath
	}

	return allTags, nil
}
