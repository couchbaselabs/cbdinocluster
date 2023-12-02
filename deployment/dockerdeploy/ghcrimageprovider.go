package dockerdeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type GhcrImageProvider struct {
	Logger       *zap.Logger
	DockerCli    *client.Client
	GhcrUsername string
	GhcrPassword string
}

var _ ImageProvider = (*GhcrImageProvider)(nil)

func (p *GhcrImageProvider) genGhcrAuthConfig() types.AuthConfig {
	return types.AuthConfig{
		Username: p.GhcrUsername,
		Password: p.GhcrPassword,
	}
}

func (p *GhcrImageProvider) genGhcrAuthStr() string {
	authConfig := p.genGhcrAuthConfig()
	authConfigJson, _ := json.Marshal(authConfig)
	return base64.StdEncoding.EncodeToString(authConfigJson)
}

func (p *GhcrImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	if p.GhcrUsername == "" && p.GhcrPassword == "" {
		return nil, errors.New("cannot use ghcr without credentials")
	}

	if def.BuildNo == 0 {
		return nil, errors.New("cannot use ghcr for ga releases")
	}

	serverVersion := fmt.Sprintf("%s-%d", def.Version, def.BuildNo)
	if def.UseCommunityEdition {
		serverVersion = "community-" + serverVersion
	}

	p.Logger.Debug("pulling image from ghcr")

	ghcrImagePath := fmt.Sprintf("ghcr.io/cb-vanilla/server:%s", serverVersion)
	p.Logger.Debug("identified ghcr image to pull", zap.String("image", ghcrImagePath))

	return MultiArchImagePuller{
		Logger:       p.Logger,
		DockerCli:    p.DockerCli,
		RegistryAuth: p.genGhcrAuthStr(),
		ImagePath:    ghcrImagePath,
	}.Pull(ctx)
}

func (p *GhcrImageProvider) ListImages(ctx context.Context) ([]deployment.Image, error) {
	dkrImages, err := p.DockerCli.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", "ghcr.io/cb-vanilla/server")),
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

			var versionName string

			versionParts := strings.Split(tagParts[1], "-")
			if len(versionParts) == 2 {
				// 7.2.2-1852
				versionName = "enterprise-" + tagParts[1]
			} else if len(versionParts) == 3 {
				// community-7.2.2-1852
				if versionParts[0] != "community" {
					return nil, fmt.Errorf("encountered unexpected image name: %s", repoTag)
				}

				versionName = tagParts[1]
			} else {
				return nil, fmt.Errorf("encountered unexpected image name: %s", repoTag)
			}

			images = append(images, deployment.Image{
				Source: "ghcr",
				Name:   versionName,
			})
		}
	}

	return images, nil
}

func (p *GhcrImageProvider) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	var respData struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	err := doRegistryGet(ctx,
		"https://ghcr.io/v2/cb-vanilla/server/tags/list?n=10000",
		"Bearer "+base64.StdEncoding.EncodeToString([]byte(p.GhcrPassword)),
		&respData)
	if err != nil {
		return nil, errors.Wrap(err, "failed to search images")
	}

	var images []deployment.Image
	for _, tagName := range respData.Tags {
		parsedParts := strings.Split(tagName, "-")
		if len(parsedParts) == 1 {
			// we ignore generic tags with no build number
			continue
		}

		if strings.Contains(tagName, "community") {
			// ignore community versions
			continue
		}

		versionName := tagName
		if !strings.Contains(versionName, version) {
			// ignore versions that don't match the search
			continue
		}

		images = append(images, deployment.Image{
			Source: "ghcr",
			Name:   versionName,
		})
	}

	return images, nil
}
