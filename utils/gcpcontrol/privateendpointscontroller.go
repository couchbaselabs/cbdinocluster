package gcpcontrol

import (
	"cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"context"
	"fmt"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/dns/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
	"regexp"
	"strings"
)

type PrivateEndpointsController struct {
	Logger    *zap.Logger
	Creds     *google.Credentials
	ProjectID string
	Region    string
}

type GetServiceAttachmentsResult struct {
	ServiceAttachment string
	BootstrapService  string
}

type CreatePrivateDNSZoneOptions struct {
	ServiceAttachments GetServiceAttachmentsResult
	BaseDnsName        string
	NetworkInterface   *computepb.NetworkInterface
	ClusterID          string
}

type CreateIpAddressAndForwardingRuleOptions struct {
	NetworkInterface        *computepb.NetworkInterface
	TargetServiceAttachment string
	IPAddressName           string
	ForwardingRuleName      string
	DnsName                 string
	ZoneName                string
}

type DnsChange struct {
	Name    string
	Type    string
	Ttl     int64
	Rrdatas []string
}

func (c *PrivateEndpointsController) GetNetworkAndSubnet(ctx context.Context, instanceID, zone string) (*computepb.NetworkInterface, error) {
	c.Logger.Info("using specified zone to find network and subnet")
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

	if len(instance.NetworkInterfaces) == 0 {
		return nil, errors.New("instance has no network interfaces")
	}
	return instance.NetworkInterfaces[0], nil
}

func (c *PrivateEndpointsController) GetServiceAttachments(command string) (*GetServiceAttachmentsResult, error) {
	serviceAttachmentPattern := regexp.MustCompile(`(?m)^SERVICE_ATTACHMENT=''([^']+)''`)
	bootstrapServicePattern := regexp.MustCompile(`(?m)^BOOTSTRAP_SERVICE=''([^']+)''`)

	serviceAttachmentMatches := serviceAttachmentPattern.FindStringSubmatch(command)
	bootstrapServiceMatches := bootstrapServicePattern.FindStringSubmatch(command)

	if len(serviceAttachmentMatches) < 2 || len(bootstrapServiceMatches) < 2 {
		return nil, fmt.Errorf("failed to extract service attachment values from command")
	}

	serviceAttachment := serviceAttachmentMatches[1]
	bootstrapService := bootstrapServiceMatches[1]

	return &GetServiceAttachmentsResult{
		ServiceAttachment: serviceAttachment,
		BootstrapService:  bootstrapService,
	}, nil
}

func (c *PrivateEndpointsController) CreateIpAddressAndForwardingRule(ctx context.Context, opts *CreateIpAddressAndForwardingRuleOptions) (*DnsChange, error) {
	c.Logger.Info("Creating IP address and forwarding rule",
		zap.String("ipAddressName", opts.IPAddressName),
		zap.String("forwardingRuleName", opts.ForwardingRuleName),
		zap.String("targetServiceAttachment", opts.TargetServiceAttachment))

	// Create compute client
	computeClient, err := compute.NewAddressesRESTClient(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create compute client")
	}
	defer computeClient.Close()

	// Create static IP address
	addressReq := &computepb.InsertAddressRequest{
		Project: c.ProjectID,
		Region:  c.Region,
		AddressResource: &computepb.Address{
			Name:        &opts.IPAddressName,
			Description: proto.String("Static IP for private endpoint"),
			AddressType: proto.String("INTERNAL"),
			Subnetwork:  opts.NetworkInterface.Subnetwork,
		},
	}

	addressOp, err := computeClient.Insert(ctx, addressReq)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create static IP address")
	}

	// Wait for the address creation to complete
	if err := addressOp.Wait(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to wait for address creation")
	}

	c.Logger.Info("Created static IP address", zap.String("ipAddressName", opts.IPAddressName))

	// Get the created address to get the IP
	getAddressReq := &computepb.GetAddressRequest{
		Project: c.ProjectID,
		Region:  c.Region,
		Address: opts.IPAddressName,
	}

	address, err := computeClient.Get(ctx, getAddressReq)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get created address")
	}

	ipAddressLink := address.GetSelfLink()
	ipAddress := address.GetAddress()
	c.Logger.Info("Retrieved IP address", zap.String("ipAddress", ipAddress))

	// Create forwarding rule
	forwardingRuleClient, err := compute.NewForwardingRulesRESTClient(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create forwarding rules client")
	}
	defer forwardingRuleClient.Close()

	forwardingRuleReq := &computepb.InsertForwardingRuleRequest{
		Project: c.ProjectID,
		Region:  c.Region,
		ForwardingRuleResource: &computepb.ForwardingRule{
			Name:        &opts.ForwardingRuleName,
			Description: proto.String("Forwarding rule for private endpoint"),
			IPAddress:   &ipAddressLink,
			Network:     opts.NetworkInterface.Network,
			Subnetwork:  opts.NetworkInterface.Subnetwork,
			Target:      &opts.TargetServiceAttachment,
		},
	}

	forwardingRuleOp, err := forwardingRuleClient.Insert(ctx, forwardingRuleReq)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create forwarding rule")
	}

	// Wait for the forwarding rule creation to complete
	if err := forwardingRuleOp.Wait(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to wait for forwarding rule creation")
	}

	c.Logger.Info("Created forwarding rule",
		zap.String("forwardingRuleName", opts.ForwardingRuleName),
		zap.String("ipAddress", ipAddress))

	// Create DNS change (equivalent to gcloud dns record-sets transaction add)
	dnsChange := &DnsChange{
		Name:    opts.DnsName,
		Type:    "A",
		Ttl:     300,
		Rrdatas: []string{ipAddress},
	}

	c.Logger.Info("Prepared DNS A record for transaction",
		zap.String("dnsName", opts.DnsName),
		zap.String("ipAddress", ipAddress))

	return dnsChange, nil
}

func (c *PrivateEndpointsController) CreatePrivateDNSZone(ctx context.Context, opts *CreatePrivateDNSZoneOptions) error {
	netInfo := opts.NetworkInterface

	clusterShort := opts.ClusterID
	if len(clusterShort) > 15 {
		clusterShort = clusterShort[:15]
	}

	managedZoneName := fmt.Sprintf("cbdc2-managed-%s", clusterShort)

	// Create DNS service with explicit credentials
	tokenSource := c.Creds.TokenSource
	dnsService, err := dns.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return errors.Wrap(err, "failed to create Cloud DNS service")
	}

	// Create the managed zone
	managedZone := &dns.ManagedZone{
		Name:        managedZoneName,
		Description: "Private Endpoint for Capella cluster",
		DnsName:     opts.BaseDnsName,
		Visibility:  "private",
		PrivateVisibilityConfig: &dns.ManagedZonePrivateVisibilityConfig{
			Networks: []*dns.ManagedZonePrivateVisibilityConfigNetwork{
				{
					NetworkUrl: *netInfo.Network,
				},
			},
		},
	}

	_, err = dnsService.ManagedZones.Create(c.ProjectID, "global", managedZone).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to create managed zone: %v", err)
	}

	c.Logger.Info("Created managed zone", zap.String("managed zone", managedZone.Name))

	// Collect DNS changes for batch execution
	var dnsChanges []*DnsChange

	// Create IP address and forwarding rule for main service attachment
	dnsChange1, err := c.CreateIpAddressAndForwardingRule(ctx, &CreateIpAddressAndForwardingRuleOptions{
		NetworkInterface:        netInfo,
		TargetServiceAttachment: opts.ServiceAttachments.ServiceAttachment,
		IPAddressName:           fmt.Sprintf("pe-address-%s", managedZoneName),
		ForwardingRuleName:      fmt.Sprintf("endpoint-%s", managedZoneName),
		DnsName:                 fmt.Sprintf("pe.%s", opts.BaseDnsName),
		ZoneName:                managedZoneName,
	})

	if err != nil {
		return errors.Wrap(err, "failed to create IP address and forwarding rule for service attachment")
	}
	dnsChanges = append(dnsChanges, dnsChange1)

	// Create IP address and forwarding rule for bootstrap service
	dnsChange2, err := c.CreateIpAddressAndForwardingRule(ctx, &CreateIpAddressAndForwardingRuleOptions{
		NetworkInterface:        netInfo,
		TargetServiceAttachment: opts.ServiceAttachments.BootstrapService,
		IPAddressName:           fmt.Sprintf("pe-address-bootstrap-%s", managedZoneName),
		ForwardingRuleName:      fmt.Sprintf("endpoint-bootstrap-%s", managedZoneName),
		DnsName:                 fmt.Sprintf("private-endpoint.%s", opts.BaseDnsName),
		ZoneName:                managedZoneName,
	})

	if err != nil {
		return errors.Wrap(err, "failed to create IP address and forwarding rule for bootstrap service")
	}
	dnsChanges = append(dnsChanges, dnsChange2)

	// Execute DNS transaction (equivalent to gcloud dns record-sets transaction execute)
	c.Logger.Info("Executing DNS transaction", zap.String("zoneName", managedZoneName))

	// Convert DnsChange structs to ResourceRecordSet
	var additions []*dns.ResourceRecordSet
	for _, change := range dnsChanges {
		additions = append(additions, &dns.ResourceRecordSet{
			Name:    change.Name,
			Type:    change.Type,
			Ttl:     change.Ttl,
			Rrdatas: change.Rrdatas,
		})
	}

	dnsChange := &dns.Change{
		Additions: additions,
	}

	_, err = dnsService.Changes.Create(c.ProjectID, "global", managedZoneName, dnsChange).Context(ctx).Do()
	if err != nil {
		return errors.Wrap(err, "failed to execute DNS transaction")
	}

	c.Logger.Info("Successfully executed DNS transaction",
		zap.String("zoneName", managedZoneName),
		zap.Int("recordCount", len(additions)))

	return nil
}

func (c *PrivateEndpointsController) removeForwardingRules(ctx context.Context, identifier string) error {
	forwardingRuleClient, err := compute.NewForwardingRulesRESTClient(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create forwarding rules client")
	}
	defer forwardingRuleClient.Close()

	// List all forwarding rules in the region
	listReq := &computepb.ListForwardingRulesRequest{
		Project: c.ProjectID,
		Region:  c.Region,
	}

	it := forwardingRuleClient.List(ctx, listReq)
	for {
		forwardingRule, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return errors.Wrap(err, "failed to list forwarding rules")
		}

		// Check if the forwarding rule name contains the identifier
		if strings.Contains(forwardingRule.GetName(), identifier) {
			c.Logger.Info("deleting forwarding rule", zap.String("name", forwardingRule.GetName()))

			deleteReq := &computepb.DeleteForwardingRuleRequest{
				Project:        c.ProjectID,
				Region:         c.Region,
				ForwardingRule: forwardingRule.GetName(),
			}

			op, err := forwardingRuleClient.Delete(ctx, deleteReq)
			if err != nil {
				c.Logger.Error("failed to delete forwarding rule",
					zap.String("name", forwardingRule.GetName()),
					zap.Error(err))
				continue
			}

			if err := op.Wait(ctx); err != nil {
				c.Logger.Error("failed to wait for forwarding rule deletion",
					zap.String("name", forwardingRule.GetName()),
					zap.Error(err))
				continue
			}

			c.Logger.Info("successfully deleted forwarding rule", zap.String("name", forwardingRule.GetName()))
		}
	}

	return nil
}

func (c *PrivateEndpointsController) removeIPAddresses(ctx context.Context, identifier string) error {
	computeClient, err := compute.NewAddressesRESTClient(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create addresses client")
	}
	defer computeClient.Close()

	// List all addresses in the region
	listReq := &computepb.ListAddressesRequest{
		Project: c.ProjectID,
		Region:  c.Region,
	}

	it := computeClient.List(ctx, listReq)
	for {
		address, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return errors.Wrap(err, "failed to list addresses")
		}

		// Check if the address name contains the identifier
		if strings.Contains(address.GetName(), identifier) {
			c.Logger.Info("deleting IP address", zap.String("name", address.GetName()))

			deleteReq := &computepb.DeleteAddressRequest{
				Project: c.ProjectID,
				Region:  c.Region,
				Address: address.GetName(),
			}

			op, err := computeClient.Delete(ctx, deleteReq)
			if err != nil {
				c.Logger.Error("failed to delete IP address",
					zap.String("name", address.GetName()),
					zap.Error(err))
				continue
			}

			if err := op.Wait(ctx); err != nil {
				c.Logger.Error("failed to wait for IP address deletion",
					zap.String("name", address.GetName()),
					zap.Error(err))
				continue
			}

			c.Logger.Info("successfully deleted IP address", zap.String("name", address.GetName()))
		}
	}

	return nil
}

func (c *PrivateEndpointsController) removeDNSZones(ctx context.Context, identifier string) error {
	tokenSource := c.Creds.TokenSource
	dnsService, err := dns.NewService(ctx, option.WithTokenSource(tokenSource))
	if err != nil {
		return errors.Wrap(err, "failed to create Cloud DNS service")
	}

	listCall := dnsService.ManagedZones.List(c.ProjectID, "global")
	managedZones, err := listCall.Context(ctx).Do()
	if err != nil {
		return errors.Wrap(err, "failed to list managed zones")
	}

	for _, zone := range managedZones.ManagedZones {
		// Check if the zone name contains the identifier
		if strings.Contains(zone.Name, identifier) {
			c.Logger.Info("deleting DNS zone", zap.String("name", zone.Name))

			if err := c.deleteRecordSets(ctx, dnsService, zone.Name); err != nil {
				c.Logger.Error("failed to delete record sets",
					zap.String("zoneName", zone.Name),
					zap.Error(err))
				continue
			}

			err := dnsService.ManagedZones.Delete(c.ProjectID, "global", zone.Name).Context(ctx).Do()
			if err != nil {
				c.Logger.Error("failed to delete DNS zone",
					zap.String("name", zone.Name),
					zap.Error(err))
				continue
			}

			c.Logger.Info("successfully deleted DNS zone", zap.String("name", zone.Name))
		}
	}

	return nil
}

func (c *PrivateEndpointsController) deleteRecordSets(ctx context.Context, dnsService *dns.Service, zoneName string) error {
	listCall := dnsService.ResourceRecordSets.List(c.ProjectID, "global", zoneName)
	recordSets, err := listCall.Context(ctx).Do()
	if err != nil {
		return errors.Wrap(err, "failed to list record sets")
	}

	var deletions []*dns.ResourceRecordSet
	for _, recordSet := range recordSets.Rrsets {
		if recordSet.Type != "NS" && recordSet.Type != "SOA" {
			deletions = append(deletions, recordSet)
		}
	}

	if len(deletions) > 0 {
		dnsChange := &dns.Change{
			Deletions: deletions,
		}

		_, err = dnsService.Changes.Create(c.ProjectID, "global", zoneName, dnsChange).Context(ctx).Do()
		if err != nil {
			return errors.Wrap(err, "failed to delete record sets")
		}

		c.Logger.Info("successfully deleted record sets from DNS zone",
			zap.String("zoneName", zoneName),
			zap.Int("recordCount", len(deletions)))
	}

	return nil
}

func (c *PrivateEndpointsController) RemovePrivateDnsZone(ctx context.Context, identifier string) error {
	c.Logger.Info("cleaning up private DNS zones", zap.String("identifierSubstring", identifier))

	// Remove Forwarding rules that contains the identifier
	if err := c.removeForwardingRules(ctx, identifier); err != nil {
		return errors.Wrap(err, "failed to remove forwarding rules")
	}

	// Remove IP addresses that contain the identifier
	if err := c.removeIPAddresses(ctx, identifier); err != nil {
		return errors.Wrap(err, "failed to remove IP addresses")
	}

	// Remove the DNS zone that contains the identifier
	if err := c.removeDNSZones(ctx, identifier); err != nil {
		return errors.Wrap(err, "failed to remove DNS zones")
	}

	return nil
}

func (c *PrivateEndpointsController) RemoveAll(ctx context.Context) error {
	c.Logger.Info("Removing all private DNS entries")

	// Remove all private DNS entries created by cbdinocluster
	return c.RemovePrivateDnsZone(ctx, "cbdc2-managed-")
}

func (c *PrivateEndpointsController) GetDNSZonesIdentifierToRemove(ctx context.Context) ([]string, error) {
	forwardingRuleClient, err := compute.NewForwardingRulesRESTClient(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create forwarding rules client")
	}
	defer forwardingRuleClient.Close()

	listReq := &computepb.ListForwardingRulesRequest{
		Project: c.ProjectID,
		Region:  c.Region,
	}

	var identifierList []string
	it := forwardingRuleClient.List(ctx, listReq)
	for {
		forwardingRule, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "failed to list forwarding rules")
		}

		ruleName := forwardingRule.GetName()
		if strings.Contains(ruleName, "cbdc2-managed-") {
			target := forwardingRule.GetTarget()
			if target != "" && strings.Contains(target, "serviceAttachments/") {
				pscConnectionStatus := forwardingRule.GetPscConnectionStatus()

				c.Logger.Debug("Checking forwarding rule PSC status",
					zap.String("ruleName", ruleName),
					zap.String("pscStatus", pscConnectionStatus))

				if pscConnectionStatus == "REJECTED" || pscConnectionStatus == "CLOSED" {
					// Extract the last 15 characters, i.e, unique cluster ID part
					currIdentifier := ruleName[len(ruleName)-15:]

					found := false
					for _, existing := range identifierList {
						if existing == currIdentifier {
							found = true
							break
						}
					}
					if !found {
						identifierList = append(identifierList, currIdentifier)
						c.Logger.Info("found DNS zone to remove due to PSC status",
							zap.String("identifierSubstring", currIdentifier),
							zap.String("pscStatus", pscConnectionStatus),
							zap.String("forwardingRule", ruleName))
					}
				}
			}
		}
	}

	return identifierList, nil
}

func (c *PrivateEndpointsController) Cleanup(ctx context.Context) error {
	identifierList, err := c.GetDNSZonesIdentifierToRemove(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to get DNS zone names to remove")
	}

	for _, identifier := range identifierList {
		if err := c.RemovePrivateDnsZone(ctx, identifier); err != nil {
			return errors.Wrapf(err, "failed to remove private DNS entries for identifier substring %s", identifier)
		}
	}

	return nil
}
