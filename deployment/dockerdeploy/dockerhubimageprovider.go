package dockerdeploy

import (
	"context"
	"fmt"

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
