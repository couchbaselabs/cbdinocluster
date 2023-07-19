package dockerdeploy

import (
	"context"

	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type HybridImageProvider struct {
	Logger       *zap.Logger
	DockerCli    *client.Client
	GhcrUsername string
	GhcrPassword string
}

var _ ImageProvider = (*HybridImageProvider)(nil)

func (p *HybridImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	dhProvider := &DockerHubImageProvider{
		Logger:    p.Logger,
		DockerCli: p.DockerCli,
	}

	ghcrProvider := &GhcrImageProvider{
		Logger:       p.Logger,
		DockerCli:    p.DockerCli,
		GhcrUsername: p.GhcrUsername,
		GhcrPassword: p.GhcrPassword,
	}

	dhServerlessProvider := &ServerlessImageProvider{
		Logger:            p.Logger,
		DockerCli:         p.DockerCli,
		BaseProviderTag:   "dh",
		BaseImageProvider: dhProvider,
	}

	ghcrServerlessProvider := &ServerlessImageProvider{
		Logger:            p.Logger,
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
			p.Logger.Debug("hybrid provider variant failed to provide image", zap.Error(err))
			continue
		}

		return image, nil
	}

	return nil, errors.New("all providers failed to provide the image")
}
