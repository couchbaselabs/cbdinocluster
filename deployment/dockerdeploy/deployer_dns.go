package dockerdeploy

import (
	"context"
	"slices"

	"go.uber.org/zap"
)

func (d *Deployer) updateDnsRecords(
	ctx context.Context,
	dnsName string,
	nodes []*nodeInfo,
	isColumnar bool,
	loadBalancerIp string,
	isNewCluster bool,
) error {
	var records []DnsRecord

	var allNodeAs []string
	var allNodeSrvs []string
	var allNodeSecSrvs []string

	for _, node := range nodes {
		if node.Type != "server-node" && node.Type != "columnar-node" {
			continue
		}

		/* don't spam the dns names for now...
		records = append(records, DnsRecord{
			RecordType: "A",
			Name:       node.DnsName,
			Addrs:      []string{node.IPAddress},
		})
		*/

		allNodeAs = append(allNodeAs, node.IPAddress)
		allNodeSrvs = append(allNodeSrvs, "0 0 11210 "+node.IPAddress)
		allNodeSecSrvs = append(allNodeSecSrvs, "0 0 11207 "+node.IPAddress)
	}

	if !isColumnar || loadBalancerIp == "" {
		records = append(records, DnsRecord{
			RecordType: "A",
			Name:       dnsName,
			Addrs:      allNodeAs,
		})
	} else {
		// no need to update the A record for the load balancer on every modify
		if isNewCluster {
			records = append(records, DnsRecord{
				RecordType: "A",
				Name:       dnsName,
				Addrs:      []string{loadBalancerIp},
			})
		}
	}

	if !isColumnar {
		records = append(records, DnsRecord{
			RecordType: "SRV",
			Name:       "_couchbase._tcp.srv." + dnsName,
			Addrs:      allNodeSrvs,
		})

		records = append(records, DnsRecord{
			RecordType: "SRV",
			Name:       "_couchbases._tcp.srv." + dnsName,
			Addrs:      allNodeSecSrvs,
		})
	}

	d.logger.Info("updating dns records", zap.Any("records", records))

	noWait := isNewCluster
	err := d.dnsProvider.UpdateRecords(ctx, records, noWait, false)
	if err != nil {
		return err
	}

	d.logger.Info("records created")
	return nil
}

func (d *Deployer) appendNodeDnsNames(dnsNames []string, node *ContainerInfo) []string {
	if node.DnsName != "" {
		if !slices.Contains(dnsNames, node.DnsName) {
			dnsNames = append(dnsNames, node.DnsName)
		}
	}
	if node.DnsSuffix != "" {
		couchbaseRec := "_couchbase._tcp.srv." + node.DnsSuffix
		couchbasesRec := "_couchbases._tcp.srv." + node.DnsSuffix

		if !slices.Contains(dnsNames, node.DnsSuffix) {
			dnsNames = append(dnsNames, node.DnsSuffix)
		}
		if !slices.Contains(dnsNames, couchbaseRec) {
			dnsNames = append(dnsNames, couchbaseRec)
		}
		if !slices.Contains(dnsNames, couchbasesRec) {
			dnsNames = append(dnsNames, couchbasesRec)
		}
	}

	return dnsNames
}

func (d *Deployer) removeDnsNames(ctx context.Context, dnsNames []string) {
	if len(dnsNames) > 0 {
		if d.dnsProvider == nil {
			d.logger.Warn("could not remove associated dns names due to no dns configuration")
		} else {
			d.logger.Info("removing dns names", zap.Any("names", dnsNames))
			err := d.dnsProvider.RemoveRecords(ctx, dnsNames, true, true)
			if err != nil {
				d.logger.Warn("failed to remove dns names", zap.Error(err))
			}
		}
	}
}
