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

type ToyImageProvider struct {
	Logger    *zap.Logger
	DockerCli *client.Client
}

var _ ImageProvider = (*ToyImageProvider)(nil)

func (p *ToyImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	if def.UseServerless {
		return nil, errors.New("cannot use toybuilds for serverless releases")
	}
	if def.UseColumnar {
		return nil, errors.New("cannot use toybuilds for columnar releases")
	}

	var edition string
	if def.UseCommunityEdition {
		edition = "community"
	} else {
		edition = "enterprise"
	}
	toyImagePath := fmt.Sprintf("build-docker.couchbase.com/couchbase/server-toy:%s-%s-%s-%d", def.Owner, edition, def.Version, def.BuildNo)

	p.Logger.Debug("identified toy build image to pull", zap.String("image", toyImagePath))

	return MultiArchImagePuller{
		Logger:    p.Logger,
		DockerCli: p.DockerCli,
		ImagePath: toyImagePath,
	}.Pull(ctx)
}

func (p *ToyImageProvider) GetImageRaw(ctx context.Context, imagePath string) (*ImageRef, error) {
	return MultiArchImagePuller{
		Logger:    p.Logger,
		DockerCli: p.DockerCli,
		ImagePath: imagePath,
	}.Pull(ctx)
}

func (p *ToyImageProvider) ListImages(ctx context.Context) ([]deployment.Image, error) {
	dkrImages, err := p.DockerCli.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", "build-docker.couchbase.com/couchbase/server-toy")),
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

			versionParts := strings.Split(tagParts[1], "-")
			if len(versionParts) != 4 {
				p.Logger.Debug("Skip image that does not look like a toy", zap.Any("repoTag", repoTag))
				continue
			}

			images = append(images, deployment.Image{
				Source:     "toy",
				Name:       tagParts[1],
				SourcePath: fmt.Sprintf("build-docker.couchbase.com/couchbase/server-toy:%s", tagParts[1]),
			})
		}
	}

	return images, nil
}

func (p *ToyImageProvider) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	allTagsUri := "http://build-docker.couchbase.com:8010/v2/couchbase/server-toy/tags/list"

	var images []deployment.Image
	p.Logger.Debug("Fetching one registry listings page", zap.String("path", allTagsUri))

	var respData struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	err := doRegistryGet(ctx, allTagsUri, "", &respData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to search images")
	}

	for _, tag := range respData.Tags {
		parsedParts := strings.Split(tag, "-")
		if len(parsedParts) != 4 {
			// we ignore tags that do not look like Owner-Edition-Version-Build
			continue
		}

		versionName := parsedParts[2]
		if !strings.Contains(versionName, version) {
			// ignore versions that don't match the search
			continue
		}

		images = append(images, deployment.Image{
			Source:     "toy",
			Name:       versionName,
			SourcePath: fmt.Sprintf("build-docker.couchbase.com/couchbase/server-toy:%s", tag),
		})
	}

	return images, nil
}
