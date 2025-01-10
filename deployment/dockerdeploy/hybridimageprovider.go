package dockerdeploy

import (
	"context"

	"github.com/couchbaselabs/cbdinocluster/deployment"
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

func (p *HybridImageProvider) getProviders() []ImageProvider {
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

	toyProvider := &ToyImageProvider{
		Logger:    p.Logger,
		DockerCli: p.DockerCli,
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

	return []ImageProvider{
		dhProvider,
		ghcrProvider,
		toyProvider,
		dhServerlessProvider,
		ghcrServerlessProvider,
	}
}

func (p *HybridImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	allProviders := p.getProviders()

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

func (p *HybridImageProvider) GetImageRaw(ctx context.Context, imagePath string) (*ImageRef, error) {
	allProviders := p.getProviders()

	for _, provider := range allProviders {
		image, err := provider.GetImageRaw(ctx, imagePath)
		if err != nil {
			p.Logger.Debug("hybrid provider variant failed to provide image", zap.Error(err))
			continue
		}

		return image, nil
	}

	return nil, errors.New("all providers failed to provide the image")
}

func (p *HybridImageProvider) ListImages(ctx context.Context) ([]deployment.Image, error) {
	allProviders := p.getProviders()

	var images []deployment.Image
	for _, provider := range allProviders {
		providerImages, err := provider.ListImages(ctx)
		if err != nil {
			p.Logger.Debug("hybrid provider variant failed to list images", zap.Error(err))
			continue
		}

		images = append(images, providerImages...)
	}

	return images, nil
}

func (p *HybridImageProvider) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	allProviders := p.getProviders()

	var images []deployment.Image
	for _, provider := range allProviders {
		providerImages, err := provider.SearchImages(ctx, version)
		if err != nil {
			p.Logger.Debug("hybrid provider variant failed to search images", zap.Error(err))
			continue
		}

		images = append(images, providerImages...)
	}

	return images, nil
}
