package gcpcontrol

import (
	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"context"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"path"
)

type PrivateEndpointsController struct {
	Logger    *zap.Logger
	Zone      string
	Creds     *google.Credentials
	ProjectID string
}

type NetworkDetails struct {
	NetworkID    string
	SubnetworkID string
}

func (c *PrivateEndpointsController) GetNetworkAndSubnet(ctx context.Context, instanceID string) (*NetworkDetails, error) {
	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create instances client")
	}
	defer instancesClient.Close()

	instance, err := instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Zone:     c.Zone,
		Instance: instanceID,
		Project:  c.ProjectID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get instance details")
	}

	if len(instance.NetworkInterfaces) == 0 {
		return nil, errors.New("instance has no network interfaces")
	}
	networkInterface := instance.NetworkInterfaces[0]
	network := path.Base(*networkInterface.Network)
	subnetwork := path.Base(*networkInterface.Subnetwork)

	return &NetworkDetails{
		NetworkID:    network,
		SubnetworkID: subnetwork,
	}, nil
}
