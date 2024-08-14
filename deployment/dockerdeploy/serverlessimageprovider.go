package dockerdeploy

import (
	"context"
	"embed"
	"fmt"
	"os"
	"strings"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/tarhelper"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

//go:embed dockerfiles
var assetsFs embed.FS

type ServerlessImageProvider struct {
	Logger            *zap.Logger
	DockerCli         *client.Client
	BaseProviderTag   string
	BaseImageProvider ImageProvider
}

var _ ImageProvider = (*ServerlessImageProvider)(nil)

func (p *ServerlessImageProvider) GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error) {
	if !def.UseServerless {
		return nil, errors.New("cannot use serverless provider for non-serverless")
	}

	if def.UseColumnar {
		return nil, errors.New("cannot use serverless provider for columnar images")
	}

	var serverVariant string
	if def.UseCommunityEdition {
		serverVariant = "community"
	} else {
		serverVariant = "enterprise"
	}

	var serverVersion string
	if def.BuildNo > 0 {
		serverVersion = fmt.Sprintf("%s-%d", def.Version, def.BuildNo)
	} else {
		serverVersion = def.Version
	}

	tagName := strings.Join([]string{"dynclst", "serverless", p.BaseProviderTag, "server"}, "-")
	tagVersion := fmt.Sprintf("%s-%s", serverVariant, serverVersion)
	fullTagPath := fmt.Sprintf("%s:%s", tagName, tagVersion)

	images, err := p.DockerCli.ImageList(ctx, types.ImageListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list images")
	}

	for _, image := range images {
		if slices.Contains(image.RepoTags, fullTagPath) {
			p.Logger.Debug("found existing image with this tag")

			return &ImageRef{
				ImagePath: fullTagPath,
			}, nil
		}
	}

	p.Logger.Debug("getting base image to use")
	baseDef := *def
	baseDef.UseServerless = false
	baseImageRef, err := p.BaseImageProvider.GetImage(ctx, &baseDef)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get base image")
	}

	p.Logger.Debug("creating temporary tar file")
	tmpTarFile, err := os.CreateTemp("", "dynclsttar")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create temp file to tar docker data")
	}
	defer tmpTarFile.Close()
	defer os.Remove(tmpTarFile.Name())

	t, err := tarhelper.NewTarBuilder(tmpTarFile)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create tar builder")
	}

	p.Logger.Debug("adding base data to tar image")
	err = t.AddEmbedDir(&assetsFs, "dockerfiles/serverless", "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to add base data")
	}

	err = t.Close()
	if err != nil {
		return nil, errors.Wrap(err, "failed to close tar builder")
	}

	tmpTarFile.Close()

	tmpRTarFile, err := os.Open(tmpTarFile.Name())
	if err != nil {
		return nil, errors.Wrap(err, "failed to open tmp tar file for reading")
	}
	defer tmpRTarFile.Close()

	p.Logger.Debug("starting image build", zap.String("image", fullTagPath))

	err = dockerBuildAndPipe(ctx, p.Logger, p.DockerCli, tmpRTarFile, types.ImageBuildOptions{
		BuildArgs: map[string]*string{
			"BASE_IMAGE": &baseImageRef.ImagePath,
		},
		Labels: map[string]string{
			"cbdyncluster": "true",
		},
		Tags: []string{fullTagPath},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to build image")
	}

	return &ImageRef{
		ImagePath: fullTagPath,
	}, nil
}

func (p *ServerlessImageProvider) GetImageRaw(ctx context.Context, imagePath string) (*ImageRef, error) {
	return nil, errors.New("serverless provider does not support raw fetches")
}

func (p *ServerlessImageProvider) ListImages(ctx context.Context) ([]deployment.Image, error) {
	return []deployment.Image{}, nil
}

func (p *ServerlessImageProvider) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	return []deployment.Image{}, nil
}
