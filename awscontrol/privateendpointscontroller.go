package awscontrol

import (
	"context"

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

func (c *PrivateEndpointsController) EnableVPCEndpointPrivateDNS(ctx context.Context, vpceID string) error {
	ec2Client := c.ec2Client()

	_, err := ec2Client.ModifyVpcEndpoint(ctx, &ec2.ModifyVpcEndpointInput{
		VpcEndpointId:     aws.String(vpceID),
		PrivateDnsEnabled: aws.Bool(true),
	})
	if err != nil {
		return errors.Wrap(err, "failed to modify vpc endpoint")
	}

	return nil
}

func (c *PrivateEndpointsController) Cleanup(ctx context.Context) error {
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
