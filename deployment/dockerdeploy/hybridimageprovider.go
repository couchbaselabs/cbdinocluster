package dockerdeploy

import (
	"context"
	"log"

	"github.com/docker/docker/client"
	"github.com/pkg/errors"
)

type HybridImageProvider struct {
	DockerCli    *client.Client
	GhcrUsername string
	GhcrPassword string
}

var _ ImageProvider = (*HybridImageProvider)(nil)

func (p *HybridImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	dhProvider := &DockerHubImageProvider{
		DockerCli: p.DockerCli,
	}

	ghcrProvider := &GhcrImageProvider{
		DockerCli:    p.DockerCli,
		GhcrUsername: p.GhcrUsername,
		GhcrPassword: p.GhcrPassword,
	}

	dhServerlessProvider := &ServerlessImageProvider{
		DockerCli:         p.DockerCli,
		BaseProviderTag:   "dh",
		BaseImageProvider: dhProvider,
	}

	ghcrServerlessProvider := &ServerlessImageProvider{
		DockerCli:         p.DockerCli,
		BaseProviderTag:   "ghcr",
		BaseImageProvider: ghcrProvider,
	}

	allProviders := []ImageProvider{
		dhProvider,
		ghcrProvider,
		dhServerlessProvider,
		ghcrServerlessProvider,
	}

	for _, provider := range allProviders {
		image, err := provider.GetImage(ctx, def)
		if err != nil {
			log.Printf("hybrid provider variant failed to provide image: %s", err)
			continue
		}

		return image, nil
	}

	return nil, errors.New("all providers failed to provide the image")
}
