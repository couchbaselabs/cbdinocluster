package awscontrol

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type LocalInstanceController struct {
	Logger *zap.Logger
}

type LocalInstanceInfo struct {
	Region     string
	InstanceID string
}

func (c *LocalInstanceController) Identify(ctx context.Context) (*LocalInstanceInfo, error) {
	imdsClient := imds.New(imds.Options{})

	instanceIdentity, err := imdsClient.GetInstanceIdentityDocument(ctx, nil)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New("must be running within an ec2 instance")
		}

		return nil, errors.Wrap(err, "failed to load instance identity data")
	}

	c.Logger.Info("instance identity loaded", zap.Any("identity", instanceIdentity))

	return &LocalInstanceInfo{
		Region:     instanceIdentity.Region,
		InstanceID: instanceIdentity.InstanceID,
	}, nil
}
