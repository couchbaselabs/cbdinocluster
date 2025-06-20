package gcpcontrol

import (
	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"context"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"log"
	"path"
	"strings"
)

type PrivateEndpointsController struct {
	Logger    *zap.Logger
	Creds     *google.Credentials
	ProjectID string
	Region    string
}

type NetworkDetails struct {
	NetworkID    string
	SubnetworkID string
}

func (c *PrivateEndpointsController) GetInstanceWithoutZone(ctx context.Context, instanceID string) (*computepb.Instance, error) {
	zonesClient, err := compute.NewZonesRESTClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create zones client: %v", err)
	}
	defer zonesClient.Close()

	zonesReq := &computepb.ListZonesRequest{
		Project: c.ProjectID,
	}
	zonesIt := zonesClient.List(ctx, zonesReq)

	var matchingZones []string
	for {
		zone, err := zonesIt.Next()
		if err != nil {
			break
		}
		if strings.HasPrefix(zone.GetName(), c.Region) {
			matchingZones = append(matchingZones, zone.GetName())
		}
	}

	// Try to find the instance in each zone
	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create instances client: %v", err)
	}
	defer instancesClient.Close()

	for _, zone := range matchingZones {
		req := &computepb.GetInstanceRequest{
			Project:  c.ProjectID,
			Zone:     zone,
			Instance: instanceID,
		}

		instance, err := instancesClient.Get(ctx, req)
		if err == nil {
			return instance, nil
		}
	}
	return nil, errors.Wrap(err, "instance not found in any zone of configured region")
}

func (c *PrivateEndpointsController) GetInstanceUsingZone(ctx context.Context, instanceID, zone string) (*computepb.Instance, error) {
	instancesClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create instances client")
	}
	defer instancesClient.Close()

	instance, err := instancesClient.Get(ctx, &computepb.GetInstanceRequest{
		Zone:     zone,
		Instance: instanceID,
		Project:  c.ProjectID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get instance details")
	}

	return instance, nil
}

func (c *PrivateEndpointsController) GetInstance(ctx context.Context, instanceID, zone string) (*computepb.Instance, error) {
	if zone == "" {
		c.Logger.Info("zone not specified, looking for instance that matched instance-id in configured region")
		return c.GetInstanceWithoutZone(ctx, instanceID)
	}
	c.Logger.Info("using specified zone to find instance")
	return c.GetInstanceUsingZone(ctx, instanceID, zone)
}

func (c *PrivateEndpointsController) GetNetworkAndSubnet(instance *computepb.Instance) (*NetworkDetails, error) {
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
