package clusterdef

import "golang.org/x/exp/slices"

type Service string

const (
	KvService        = Service("kv")
	QueryService     = Service("n1ql")
	IndexService     = Service("index")
	SearchService    = Service("fts")
	AnalyticsService = Service("cbas")
	EventingService  = Service("eventing")
	BackupService    = Service("backup")
)

func CompareServices(a, b []Service) int {
	if len(a) < len(b) {
		return -1
	} else if len(a) > len(b) {
		return +1
	}

	// copy, sort and compare the slices
	ac := slices.Clone(a)
	bc := slices.Clone(b)
	slices.Sort(ac)
	slices.Sort(bc)
	return slices.Compare(ac, bc)
}

func NsServiceToService(service string) (Service, error) {
	// we already have them as the ns server names
	return Service(service), nil
}

func ServiceToNsService(service Service) (string, error) {
	// we already have them as the ns server names
	return string(service), nil
}

func ServicesToNsServices(services []Service) ([]string, error) {
	var out []string
	for _, service := range services {
		serviceStr, err := ServiceToNsService(service)
		if err != nil {
			return nil, err
		}

		out = append(out, serviceStr)
	}
	return out, nil
}

func NsServicesToServices(services []string) ([]Service, error) {
	var out []Service
	for _, service := range services {
		service, err := NsServiceToService(service)
		if err != nil {
			return nil, err
		}

		out = append(out, service)
	}
	return out, nil
}
