package azurecontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

type PrivateEndpointsController struct {
	Logger *zap.Logger
	Region string
	Creds  azcore.TokenCredential
	SubID  string
	RgName string
}

type CreateVPCEndpointOptions struct {
	ClusterID    string
	ServiceID    string
	VmResourceID string
}

type CreateVPCEndpointResult struct {
	PeResourceID string
	PeName       string
}

func (c *PrivateEndpointsController) CreateVPCEndpoint(ctx context.Context, opts *CreateVPCEndpointOptions) (*CreateVPCEndpointResult, error) {
	vmResInfo, err := arm.ParseResourceID(opts.VmResourceID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse vm resource id")
	}

	vmName := vmResInfo.Name
	subId := vmResInfo.SubscriptionID
	rgName := vmResInfo.ResourceGroupName

	if c.SubID != subId {
		return nil, errors.New("virtual machine is not in expected subscription")
	}
	if c.RgName != rgName {
		return nil, errors.New("virtual machine is not in expected resource-group")
	}

	computeClient, err := armcompute.NewVirtualMachinesClient(subId, c.Creds, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create virtual machines client")
	}

	vmData, err := computeClient.Get(ctx, rgName, vmName, &armcompute.VirtualMachinesClientGetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get virtual machine info")
	}

	c.Logger.Debug("got virtual machine data", zap.Any("vmData", vmData))

	vmLocation := *vmData.Location
	if strings.EqualFold(c.Region, vmLocation) {
		return nil, errors.New("virtual machine is not in expected region")
	}

	if len(vmData.Properties.NetworkProfile.NetworkInterfaces) < 1 {
		return nil, errors.New("vm must have at least one NIC to use private links")
	}
	defaultNic := vmData.Properties.NetworkProfile.NetworkInterfaces[0]

	nicID := *defaultNic.ID
	nicResInfo, _ := arm.ParseResourceID(nicID)
	nicName := nicResInfo.Name

	c.Logger.Debug("identified nic", zap.Any("nicName", nicName))

	nicClient, err := armnetwork.NewInterfacesClient(subId, c.Creds, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create interfaces client")
	}

	nicData, err := nicClient.Get(ctx, rgName, nicName, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get default nic info")
	}

	c.Logger.Debug("got nic data", zap.Any("nicData", nicData))

	if len(nicData.Properties.IPConfigurations) < 1 {
		return nil, errors.New("nic was missing an ip configuration")
	}

	defaultIPConfig := nicData.Properties.IPConfigurations[0]
	defaultSubnet := defaultIPConfig.Properties.Subnet

	peClient, err := armnetwork.NewPrivateEndpointsClient(subId, c.Creds, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create private endpoints client")
	}

	c.Logger.Debug("creating private endpoint")

	peName := fmt.Sprintf("pl-%s", uuid.NewString())

	createPoller, err := peClient.BeginCreateOrUpdate(ctx, rgName, peName, armnetwork.PrivateEndpoint{
		Location: to.Ptr(c.Region),
		Tags: map[string]*string{
			"Cbdc2ClusterId": to.Ptr(opts.ClusterID),
		},
		Properties: &armnetwork.PrivateEndpointProperties{
			ManualPrivateLinkServiceConnections: []*armnetwork.PrivateLinkServiceConnection{
				{
					Name: to.Ptr(peName),
					Properties: &armnetwork.PrivateLinkServiceConnectionProperties{
						PrivateLinkServiceID: to.Ptr(opts.ServiceID),
					},
				},
			},
			Subnet: defaultSubnet,
		},
	}, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to begin creating private endpoint")
	}

	c.Logger.Debug("waiting for private endpoint creation")

	createResp, err := createPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create private endpoint")
	}

	peResourceID := *createResp.ID

	return &CreateVPCEndpointResult{
		PeResourceID: peResourceID,
		PeName:       peName,
	}, nil
}

type EnableVPCEndpointPrivateDNSOptions struct {
	ClusterID    string
	PeResourceID string
	DnsName      string
}

func (c *PrivateEndpointsController) EnableVPCEndpointPrivateDNS(ctx context.Context, opts *EnableVPCEndpointPrivateDNSOptions) error {
	peResInfo, err := arm.ParseResourceID(opts.PeResourceID)
	if err != nil {
		return errors.Wrap(err, "failed to parse private endpoint resource id")
	}

	peName := peResInfo.Name
	rgName := peResInfo.ResourceGroupName
	subId := peResInfo.SubscriptionID

	peClient, err := armnetwork.NewPrivateEndpointsClient(subId, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create private endpoints client")
	}

	peData, err := peClient.Get(ctx, rgName, peName, nil)
	if err != nil {
		return errors.Wrap(err, "failed to get private endpoint info")
	}

	c.Logger.Debug("private endpoint info", zap.Any("peData", peData))

	if len(peData.Properties.NetworkInterfaces) < 1 {
		return errors.New("private endpoint had no nics")
	}

	peNic := peData.Properties.NetworkInterfaces[0]

	peNicID := *peNic.ID
	peNicResInfo, _ := arm.ParseResourceID(peNicID)
	peNicName := peNicResInfo.Name

	nicClient, err := armnetwork.NewInterfacesClient(subId, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create interfaces client")
	}

	peNicData, err := nicClient.Get(ctx, rgName, peNicName, nil)
	if err != nil {
		return errors.Wrap(err, "failed to get private endpoint nic info")
	}

	c.Logger.Debug("got private endpoint nic info", zap.Any("peNicData", peNicData))

	if len(peNicData.Properties.IPConfigurations) < 1 {
		return errors.New("private endpoint had no ip configurations")
	}

	peNicIPConfig := peNicData.Properties.IPConfigurations[0]
	peNicIPAddr := *peNicIPConfig.Properties.PrivateIPAddress
	peNicSubnetID := *peNicIPConfig.Properties.Subnet.ID

	c.Logger.Debug("identified private endpoint ip address", zap.String("addr", peNicIPAddr))

	pdnsClient, err := armprivatedns.NewPrivateZonesClient(subId, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create private dns client")
	}

	c.Logger.Debug("creating private dns zone")

	pdnsPoller, err := pdnsClient.BeginCreateOrUpdate(ctx, rgName, opts.DnsName, armprivatedns.PrivateZone{
		Location: to.Ptr("global"),
		Tags: map[string]*string{
			"Cbdc2ClusterId":            to.Ptr(opts.ClusterID),
			"AssociatedPrivateEndpoint": to.Ptr(peName),
		},
		Properties: &armprivatedns.PrivateZoneProperties{},
	}, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin creating private dns zone")
	}

	pdnsRes, err := pdnsPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create private dns zone")
	}

	c.Logger.Debug("created private dns zone", zap.Any("pdnsRes", pdnsRes))

	rsetClient, err := armprivatedns.NewRecordSetsClient(subId, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create record set client")
	}

	c.Logger.Debug("creating record set")

	rsetRes, err := rsetClient.CreateOrUpdate(ctx, rgName, opts.DnsName, armprivatedns.RecordTypeA, "@", armprivatedns.RecordSet{
		Properties: &armprivatedns.RecordSetProperties{
			ARecords: []*armprivatedns.ARecord{
				{
					IPv4Address: to.Ptr(peNicIPAddr),
				},
			},
			TTL: to.Ptr[int64](1 * 60 * 60),
		},
	}, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create record set")
	}

	c.Logger.Debug("created record set", zap.Any("rsetRes", rsetRes))

	vnetLinksClient, err := armprivatedns.NewVirtualNetworkLinksClient(subId, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create virtual network links client")
	}

	peSubnetResInfo, _ := arm.ParseResourceID(peNicSubnetID)
	peVnetID := peSubnetResInfo.Parent.String()

	vnetLinkName := fmt.Sprintf("dnslink-%s", uuid.NewString())

	c.Logger.Debug("linking private dns zone to vnet")

	pelinkPoller, err := vnetLinksClient.BeginCreateOrUpdate(ctx, rgName, opts.DnsName, vnetLinkName, armprivatedns.VirtualNetworkLink{
		Location: to.Ptr("global"),
		Properties: &armprivatedns.VirtualNetworkLinkProperties{
			RegistrationEnabled: to.Ptr(false),
			VirtualNetwork:      &armprivatedns.SubResource{ID: to.Ptr(peVnetID)},
		},
	}, nil)
	if err != nil {
		return errors.Wrap(err, "failed to begin creating vnet link")
	}

	vnetLinkRes, err := pelinkPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create vnet link")
	}

	c.Logger.Debug("created vnet link", zap.Any("vnetLinkRes", vnetLinkRes))

	return nil
}

func (c *PrivateEndpointsController) cleanupPrivateEndpoints(ctx context.Context) error {
	peClient, err := armnetwork.NewPrivateEndpointsClient(c.SubID, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create private endpoints client")
	}

	var endpointNamesToRemove []string

	pePager := peClient.NewListPager(c.RgName, nil)
	for pePager.More() {
		page, err := pePager.NextPage(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to get next private endpoint page")
		}

		for _, peLink := range page.Value {
			c.Logger.Info("pelink", zap.Any("link", peLink))

			clusterIdPtr := peLink.Tags["Cbdc2ClusterId"]
			if clusterIdPtr == nil || *clusterIdPtr == "" {
				// this is not a cbdc managed link
				continue
			}

			peConnection := peLink.Properties.ManualPrivateLinkServiceConnections[0]
			peConnState := peConnection.Properties.PrivateLinkServiceConnectionState

			if *peConnState.Status != "Rejected" && *peConnState.Status != "Disconnected" {
				// this connection is still active
				continue
			}

			endpointNamesToRemove = append(endpointNamesToRemove, *peLink.Name)
		}
	}

	c.Logger.Info("found private endpoints to remove", zap.Strings("endpoint-names", endpointNamesToRemove))

	for _, peName := range endpointNamesToRemove {
		c.Logger.Info("removing private endpoint", zap.String("pe-name", peName))

		deleteWait, err := peClient.BeginDelete(ctx, c.RgName, peName, nil)
		if err != nil {
			return errors.Wrap(err, "failed to begin deleting private endpoint")
		}

		_, err = deleteWait.PollUntilDone(ctx, nil)
		if err != nil {
			return errors.Wrap(err, "failed to delete private endpoint")
		}
	}

	return nil
}

func (c *PrivateEndpointsController) getAllPrivateEndpointNames(ctx context.Context) ([]string, error) {
	var peNames []string

	peClient, err := armnetwork.NewPrivateEndpointsClient(c.SubID, c.Creds, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create private endpoints client")
	}

	pePager := peClient.NewListPager(c.RgName, nil)
	for pePager.More() {
		page, err := pePager.NextPage(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get next private endpoint page")
		}

		for _, peLink := range page.Value {
			peNames = append(peNames, *peLink.Name)
		}
	}

	return peNames, nil
}

func (c *PrivateEndpointsController) cleanupPrivateDns(ctx context.Context) error {
	validPeNames, err := c.getAllPrivateEndpointNames(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list all private endpoint names")
	}

	var zonesToRemove []string

	pdnsClient, err := armprivatedns.NewPrivateZonesClient(c.SubID, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create private dns client")
	}

	pdnsPager := pdnsClient.NewListPager(nil)
	for pdnsPager.More() {
		pdnsList, err := pdnsPager.NextPage(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to get next private dns page")
		}

		for _, pdnsZone := range pdnsList.Value {
			clusterIdPtr := pdnsZone.Tags["Cbdc2ClusterId"]
			if clusterIdPtr == nil || *clusterIdPtr == "" {
				// this is not a cbdc managed dns zone
				continue
			}

			peNamePtr := pdnsZone.Tags["AssociatedPrivateEndpoint"]
			if peNamePtr == nil || *peNamePtr == "" {
				// this is missing a link, so we can't clean it up
				continue
			}

			peName := *peNamePtr

			if slices.Contains(validPeNames, peName) {
				// the associated private endpoint is still available
				continue
			}

			zonesToRemove = append(zonesToRemove, *pdnsZone.Name)
		}
	}

	c.Logger.Info("found private zones to remove",
		zap.Strings("zone-names", zonesToRemove))

	var linksToRemove []string

	vnetLinksClient, err := armprivatedns.NewVirtualNetworkLinksClient(c.SubID, c.Creds, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create virtual network links client")
	}

	for _, zoneName := range zonesToRemove {
		linkPager := vnetLinksClient.NewListPager(c.RgName, zoneName, nil)
		for linkPager.More() {
			linkPage, err := linkPager.NextPage(ctx)
			if err != nil {
				return errors.Wrap(err, "failed to get next network link page")
			}

			for _, link := range linkPage.Value {
				linksToRemove = append(linksToRemove, zoneName+"/"+*link.Name)
			}
		}
	}

	c.Logger.Info("found zone links to remove",
		zap.Strings("zone-links", linksToRemove))

	for _, zoneLinkName := range linksToRemove {
		zoneLinkParts := strings.Split(zoneLinkName, "/")
		zoneName := zoneLinkParts[0]
		linkName := zoneLinkParts[1]

		c.Logger.Info("deleting link",
			zap.String("zone-name", zoneLinkName),
			zap.String("link-name", linkName))

		waiter, err := vnetLinksClient.BeginDelete(ctx, c.RgName, zoneName, linkName, nil)
		if err != nil {
			err = errors.Wrap(err, "failed to begin operation")
		} else {
			_, err = waiter.PollUntilDone(ctx, nil)
		}
		if err != nil {
			return errors.Wrap(err, "failed to delete link")
		}
	}

	for _, zoneName := range zonesToRemove {
		// azure is sometimes slow to detect the nested resource deletion so we need to
		// do a retry loop here until it works...
		for retryNum := 0; ; retryNum++ {
			c.Logger.Info("deleting zone", zap.String("zone-name", zoneName), zap.Int("retry-num", retryNum))

			waiter, err := pdnsClient.BeginDelete(ctx, c.RgName, zoneName, nil)
			if err != nil {
				err = errors.Wrap(err, "failed to begin operation")
			} else {
				_, err = waiter.PollUntilDone(ctx, nil)
			}
			if err != nil {
				if retryNum < 10 {
					time.Sleep(1 * time.Second)
					continue
				}

				return errors.Wrap(err, "failed to delete zone")
			}

			break
		}
	}

	return nil
}

func (c *PrivateEndpointsController) Cleanup(ctx context.Context) error {
	err := c.cleanupPrivateEndpoints(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to cleanup private endpoints")
	}

	err = c.cleanupPrivateDns(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to cleanup private dns")
	}

	return nil
}
