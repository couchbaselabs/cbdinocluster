package dockerdeploy

import "context"

type DnsRecord struct {
	RecordType string
	Name       string
	Addrs      []string
}

type DnsProvider interface {
	GetHostname() string
	UpdateRecords(ctx context.Context, records []DnsRecord, noWait bool, noWaitPropagate bool) error
	RemoveRecords(ctx context.Context, recordNames []string, noWait bool, noWaitPropagate bool) error
}
