package awscontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type PrivateEndpointsController struct {
	Logger      *zap.Logger
	Region      string
	Credentials aws.Credentials
}

func (c *PrivateEndpointsController) ec2Client() *ec2.Client {
	return ec2.New(ec2.Options{
		Region: c.Region,
		Credentials: aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return c.Credentials, nil
		}),
	})
}

type CreateVPCEndpointOptions struct {
	ClusterID   string
	ServiceName string
	InstanceID  string
}

type CreateVPCEndpointResult struct {
	EndpointID string
}

func (c *PrivateEndpointsController) CreateVPCEndpoint(ctx context.Context, opts *CreateVPCEndpointOptions) (*CreateVPCEndpointResult, error) {
	ec2Client := c.ec2Client()

	describeResp, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{opts.InstanceID},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to describe local instance")
	}

	var instances []types.Instance
	for _, reservation := range describeResp.Reservations {
		instances = append(instances, reservation.Instances...)
	}

	if len(instances) == 0 {
		return nil, errors.Wrap(err, "failed to find local instance")
	}

	instance := instances[0]
	vpcID := *instance.VpcId
	subnetID := *instance.SubnetId

	vpcEpResp, err := ec2Client.CreateVpcEndpoint(ctx, &ec2.CreateVpcEndpointInput{
		ServiceName:     aws.String(opts.ServiceName),
		VpcId:           aws.String(vpcID),
		SubnetIds:       []string{subnetID},
		VpcEndpointType: types.VpcEndpointTypeInterface,
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeVpcEndpoint,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("cbdc2_" + opts.ClusterID),
					},
					{
						Key:   aws.String("Cbdc2ClusterId"),
						Value: aws.String(opts.ClusterID),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create vpc endpoint")
	}

	return &CreateVPCEndpointResult{
		EndpointID: *vpcEpResp.VpcEndpoint.VpcEndpointId,
	}, nil
}

func (c *PrivateEndpointsController) WaitForVPCEndpointStatus(ctx context.Context, vpceID string, desiredState string) error {
	ec2Client := c.ec2Client()

	MISSING_STATE := "*MISSING*"
	if desiredState == "" {
		// a blank desired state means to wait until it's deleted...
		desiredState = MISSING_STATE
	}

	for {
		vpcEndpoints, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
			VpcEndpointIds: []string{vpceID},
		})

		if err != nil {
			return errors.Wrap(err, "failed to list vpc endpoints")
		}

		endpointStatus := ""
		for _, endpoint := range vpcEndpoints.VpcEndpoints {
			if *endpoint.VpcEndpointId == vpceID {
				endpointStatus = string(endpoint.State)
			}
		}

		if endpointStatus == "" {
			endpointStatus = MISSING_STATE
		}

		if endpointStatus == MISSING_STATE && desiredState != MISSING_STATE {
			return fmt.Errorf("endpoint disappeared during wait for '%s' state", desiredState)
		}

		c.Logger.Info("waiting for private endpoint status...",
			zap.String("current", endpointStatus),
			zap.String("desired", desiredState))

		if endpointStatus != desiredState {
			time.Sleep(5 * time.Second)
			continue
		}

		break
	}

	return nil
}

type EnableVPCEndpointPrivateDNSOptions struct {
	VpceID string
}

func (c *PrivateEndpointsController) EnableVPCEndpointPrivateDNS(ctx context.Context, opts *EnableVPCEndpointPrivateDNSOptions) error {
	ec2Client := c.ec2Client()

	_, err := ec2Client.ModifyVpcEndpoint(ctx, &ec2.ModifyVpcEndpointInput{
		VpcEndpointId:     aws.String(opts.VpceID),
		PrivateDnsEnabled: aws.Bool(true),
	})
	if err != nil {
		return errors.Wrap(err, "failed to modify vpc endpoint")
	}

	err = c.WaitForVPCEndpointStatus(ctx, opts.VpceID, "available")
	if err != nil {
		return errors.Wrap(err, "failed to wait for available state")
	}

	// its safer for us to sleep here for 10s than to accidentally fetch the DNS
	// entry before its been propagated and then need to wait 5m for it to update.
	time.Sleep(10 * time.Second)

	return nil
}

// cleanupVpcEndpoints lists all the vpc endpoints in the account and then removes any
// that are tagged with Cbdc2ClusterId and which are rejected or failed.
func (c *PrivateEndpointsController) cleanupVpcEndpoints(ctx context.Context) error {
	ec2Client := c.ec2Client()

	endpoints, err := ec2Client.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{})
	if err != nil {
		return errors.Wrap(err, "failed to list vpc endpoints")
	}

	var endpointIdsToRemove []string

	for _, endpoint := range endpoints.VpcEndpoints {
		hasTag := func(tagName string) bool {
			for _, tag := range endpoint.Tags {
				if *tag.Key == tagName {
					return true
				}
			}
			return false
		}

		if !hasTag("Cbdc2ClusterId") {
			continue
		}

		if endpoint.State != "rejected" && endpoint.State != "failed" {
			continue
		}

		endpointIdsToRemove = append(endpointIdsToRemove, *endpoint.VpcEndpointId)
	}

	c.Logger.Info("found vpc endpoints to remove", zap.Strings("endpoint-ids", endpointIdsToRemove))

	if len(endpointIdsToRemove) > 0 {
		c.Logger.Info("removing vpc endpoints", zap.Strings("endpoint-ids", endpointIdsToRemove))

		_, err = ec2Client.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{
			VpcEndpointIds: endpointIdsToRemove,
		})
		if err != nil {
			return errors.Wrap(err, "failed to remove endpoints")
		}

		c.Logger.Info("removed endpoints")
	}

	return nil
}

func (c *PrivateEndpointsController) Cleanup(ctx context.Context) error {
	err := c.cleanupVpcEndpoints(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to cleanup vpc endpoints")
	}

	return nil
}
