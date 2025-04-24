package dockerdeploy

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
	"go.uber.org/zap"
	"k8s.io/utils/ptr"
)

const DNS_TTL_TIME int64 = 5

type AwsDnsProvider struct {
	Logger      *zap.Logger
	Region      string
	Credentials aws.Credentials
	Hostname    string
}

var _ DnsProvider = &AwsDnsProvider{}

func (a *AwsDnsProvider) getRoute53Client(ctx context.Context) *route53.Client {
	return route53.New(route53.Options{
		Region: a.Region,
		Credentials: aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
			return a.Credentials, nil
		}),
	})
}

func (a *AwsDnsProvider) GetHostname() string {
	return a.Hostname
}

func (a *AwsDnsProvider) getHostedZoneId(ctx context.Context) (string, error) {
	client := a.getRoute53Client(ctx)

	zones, err := client.ListHostedZones(ctx, &route53.ListHostedZonesInput{})
	if err != nil {
		return "", fmt.Errorf("failed to list hosted zones: %w", err)
	}

	for _, zone := range zones.HostedZones {
		zoneName := strings.TrimSuffix(*zone.Name, ".")
		if zoneName == a.Hostname {
			return *zone.Id, nil
		}
	}

	return "", fmt.Errorf("hosted zone not found for hostname: %s", a.Hostname)
}

func (a *AwsDnsProvider) waitForChangeId(
	ctx context.Context,
	changeId string,
	noWait bool,
	noWaitPropagate bool,
) error {
	if noWait {
		return nil
	}

	client := a.getRoute53Client(ctx)

	for {
		change, err := client.GetChange(ctx, &route53.GetChangeInput{
			Id: &changeId,
		})
		if err != nil {
			return fmt.Errorf("failed to get change status: %w", err)
		}

		changeStatus := change.ChangeInfo.Status

		a.Logger.Info("waiting for dns records to be in sync...",
			zap.String("current", string(changeStatus)))

		if changeStatus != types.ChangeStatusInsync {
			time.Sleep(5 * time.Second)
			continue
		}

		break
	}

	if !noWaitPropagate {
		a.Logger.Info("waiting for dns records to propagate (theoretically)")
		time.Sleep(time.Duration(DNS_TTL_TIME) * time.Second)
	}

	return nil
}

func (a *AwsDnsProvider) UpdateRecords(
	ctx context.Context,
	records []DnsRecord,
	noWait bool,
	noWaitPropagate bool,
) error {
	client := a.getRoute53Client(ctx)

	hostedZoneId, err := a.getHostedZoneId(ctx)
	if err != nil {
		return fmt.Errorf("failed to get hosted zone ID: %w", err)
	}

	var changes []types.Change
	for _, record := range records {
		var awsRecType types.RRType
		if record.RecordType == "A" {
			awsRecType = types.RRTypeA
		} else if record.RecordType == "CNAME" {
			awsRecType = types.RRTypeCname
		} else if record.RecordType == "SRV" {
			awsRecType = types.RRTypeSrv
		} else {
			return fmt.Errorf("unsupported record type: %s", record.RecordType)
		}

		var records []types.ResourceRecord
		for _, addr := range record.Addrs {
			records = append(records, types.ResourceRecord{
				Value: &addr,
			})
		}

		changes = append(changes, types.Change{
			Action: types.ChangeActionUpsert,
			ResourceRecordSet: &types.ResourceRecordSet{
				Name:            &record.Name,
				Type:            awsRecType,
				TTL:             ptr.To(DNS_TTL_TIME),
				ResourceRecords: records,
			},
		})
	}

	if len(changes) == 0 {
		return nil
	}

	resp, err := client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &hostedZoneId,
		ChangeBatch: &types.ChangeBatch{
			Changes: changes,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to change resource record sets: %w", err)
	}

	return a.waitForChangeId(ctx, *resp.ChangeInfo.Id, noWait, noWaitPropagate)
}

func (a *AwsDnsProvider) RemoveRecords(
	ctx context.Context,
	recordNames []string,
	noWait bool,
	noWaitPropagate bool,
) error {
	client := a.getRoute53Client(ctx)

	hostedZoneId, err := a.getHostedZoneId(ctx)
	if err != nil {
		return fmt.Errorf("failed to get hosted zone ID: %w", err)
	}

	records, err := client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: ptr.To(hostedZoneId),
	})
	if err != nil {
		return fmt.Errorf("failed to list resource record sets: %w", err)
	}

	var recordsToDelete []types.ResourceRecordSet
	for _, record := range records.ResourceRecordSets {
		recordName := strings.TrimSuffix(*record.Name, ".")
		if slices.Contains(recordNames, recordName) {
			recordsToDelete = append(recordsToDelete, record)
		}
	}

	var changes []types.Change
	for _, record := range recordsToDelete {
		changes = append(changes, types.Change{
			Action:            types.ChangeActionDelete,
			ResourceRecordSet: &record,
		})
	}

	if len(changes) == 0 {
		return nil
	}

	resp, err := client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: &hostedZoneId,
		ChangeBatch: &types.ChangeBatch{
			Changes: changes,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to change resource record sets: %w", err)
	}

	return a.waitForChangeId(ctx, *resp.ChangeInfo.Id, noWait, noWaitPropagate)
}
