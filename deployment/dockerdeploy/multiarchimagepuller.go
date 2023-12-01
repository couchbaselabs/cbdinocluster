package dockerdeploy

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type MultiArchImagePuller struct {
	Logger       *zap.Logger
	DockerCli    *client.Client
	RegistryAuth string
	ImagePath    string
}

func (p MultiArchImagePuller) Pull(ctx context.Context) (*ImageRef, error) {
	images, err := p.DockerCli.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", p.ImagePath)),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list images")
	}

	if len(images) > 0 {
		imageId := images[0].ID
		p.Logger.Debug("identified image", zap.String("imageId", imageId))
		return &ImageRef{ImagePath: imageId}, nil
	}

	p.Logger.Debug("image is not available locally, attempting to pull")

	err = dockerPullAndPipe(ctx, p.Logger, p.DockerCli, p.ImagePath, types.ImagePullOptions{
		RegistryAuth: p.RegistryAuth,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull from dockerhub registry")
	}

	images, err = p.DockerCli.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", p.ImagePath)),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list images after pull")
	}

	if len(images) > 0 {
		imageId := images[0].ID
		p.Logger.Debug("identified image", zap.String("imageId", imageId))
		return &ImageRef{ImagePath: imageId}, nil
	}

	p.Logger.Debug("image is still not available locally, attempting to pull amd64 image")

	err = dockerPullAndPipe(ctx, p.Logger, p.DockerCli, p.ImagePath, types.ImagePullOptions{
		Platform:     "linux/amd64",
		RegistryAuth: p.RegistryAuth,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull from dockerhub registry")
	}

	images, err = p.DockerCli.ImageList(ctx, types.ImageListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", p.ImagePath)),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list images after amd64 pull")
	}

	if len(images) > 0 {
		imageId := images[0].ID
		p.Logger.Debug("identified image", zap.String("imageId", imageId))
		return &ImageRef{ImagePath: imageId}, nil
	}

	return nil, errors.New("could not find referenced image")
}
