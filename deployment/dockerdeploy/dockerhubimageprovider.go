package dockerdeploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type DockerHubImageProvider struct {
	Logger    *zap.Logger
	DockerCli *client.Client
}

var _ ImageProvider = (*DockerHubImageProvider)(nil)

func (p *DockerHubImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	if def.BuildNo != 0 {
		return nil, errors.New("cannot use dockerhub for non-ga releases")
	}

	if def.UseServerless {
		return nil, errors.New("cannot use dockerhub for serverless releases")
	}

	var serverVersion string
	if def.UseCommunityEdition {
		serverVersion = fmt.Sprintf("community-%s", def.Version)
	} else {
		serverVersion = fmt.Sprintf("enterprise-%s", def.Version)
	}

	dhImagePath := fmt.Sprintf("couchbase:%s", serverVersion)
	p.Logger.Debug("identified dockerhub image to pull", zap.String("image", dhImagePath))

	return MultiArchImagePuller{
		Logger:    p.Logger,
		DockerCli: p.DockerCli,
		ImagePath: dhImagePath,
	}.Pull(ctx)
}

func (p *DockerHubImageProvider) ListImages(ctx context.Context) ([]deployment.Image, error) {
	dkrImages, err := p.DockerCli.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", "couchbase")),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list images")
	}

	var images []deployment.Image
	for _, image := range dkrImages {
		for _, repoTag := range image.RepoTags {
			tagParts := strings.Split(repoTag, ":")
			if len(tagParts) != 2 {
				return nil, fmt.Errorf("encountered unexpected image name: %s", repoTag)
			}

			versionName := tagParts[1]

			images = append(images, deployment.Image{
				Source:     "dockerhub",
				Name:       versionName,
				SourcePath: fmt.Sprintf("couchbase:%s", versionName),
			})
		}
	}

	return images, nil
}

func (p *DockerHubImageProvider) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	nextPath := "https://hub.docker.com/v2/namespaces/library/repositories/couchbase/tags?page_size=100"

	var images []deployment.Image
	for nextPath != "" {
		p.Logger.Debug("Fetching one registry listings page", zap.String("path", nextPath))

		var respData struct {
			Next    string `json:"next"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		}
		err := doRegistryGet(ctx, nextPath, "", &respData)
		if err != nil {
			return nil, errors.Wrap(err, "failed to search images")
		}

		for _, tag := range respData.Results {
			parsedParts := strings.Split(tag.Name, "-")
			if len(parsedParts) != 2 {
				// we ignore tags without the enterprise/community prefix
				continue
			}

			if parsedParts[0] != "enterprise" {
				// we only consider enterprise builds
				continue
			}

			versionName := parsedParts[1]
			if !strings.Contains(versionName, version) {
				// ignore versions that don't match the search
				continue
			}

			images = append(images, deployment.Image{
				Source:     "dockerhub",
				Name:       versionName,
				SourcePath: fmt.Sprintf("couchbase:%s", versionName),
			})
		}

		nextPath = respData.Next
	}

	return images, nil
}
