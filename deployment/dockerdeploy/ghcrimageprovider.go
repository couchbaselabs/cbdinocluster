package dockerdeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
)

type GhcrImageProvider struct {
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

	log.Printf("pulling image from ghcr")
	ghcrImagePath := fmt.Sprintf("ghcr.io/cb-vanilla/server:%s", serverVersion)
	err := dockerPullAndPipe(ctx, p.DockerCli, ghcrImagePath, types.ImagePullOptions{
		RegistryAuth: p.genGhcrAuthStr(),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull from ghcr registry")
	}

	return &ImageRef{
		ImagePath: ghcrImagePath,
	}, nil
}
