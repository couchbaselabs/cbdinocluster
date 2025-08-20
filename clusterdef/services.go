package clusterdef

import (
	"errors"
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"

	"golang.org/x/exp/slices"
)

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

func ServicesToNsServicesOverride(services []Service) ([]capellacontrol.CreateServices, error) {
	var out []capellacontrol.CreateServices
	for _, service := range services {
		serviceStr, err := ServiceToNsService(service)
		if err != nil {
			return nil, err
		}
		service := capellacontrol.CreateServices{
			Type: serviceStr,
		}
		out = append(out, service)
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

func CaoServiceToService(service string) (Service, error) {
	switch service {
	case "data":
		return KvService, nil
	case "index":
		return IndexService, nil
	case "query":
		return QueryService, nil
	case "search":
		return SearchService, nil
	case "eventing":
		return EventingService, nil
	case "analytics":
		return AnalyticsService, nil
	}
	return "", errors.New("invalid service type")
}

func ServiceToCaoService(service Service) (string, error) {
	switch service {
	case KvService:
		return "data", nil
	case IndexService:
		return "index", nil
	case QueryService:
		return "query", nil
	case SearchService:
		return "search", nil
	case EventingService:
		return "eventing", nil
	case AnalyticsService:
		return "analytics", nil
	}
	return "", errors.New("invalid service type")
}

func ServicesToCaoServices(services []Service) ([]string, error) {
	var out []string
	for _, service := range services {
		serviceStr, err := ServiceToCaoService(service)
		if err != nil {
			return nil, err
		}

		out = append(out, serviceStr)
	}
	return out, nil
}

func ServicesToPorts(services []string) ([]int, error) {
	var out []int
	for _, serviceName := range services {
		service, err := NsServiceToService(serviceName)
		if err != nil {
			return nil, fmt.Errorf("invalid service type: %s (%v)", service, err)
		}
		switch service {
		case KvService:
			out = append(out,
				/* kv */ 11210,
				/* kvSSL */ 11207,
			)
		case IndexService:
			out = append(out,
				/* indexAdmin */ 9100,
				/* indexHttp */ 9102,
				/* indexHttps */ 19102,
				/* indexScan */ 9101,
				/* indexStreamCatchup */ 9104,
				/* indexStreamInit */ 9103,
				/* indexStreamMaint */ 9105,
			)
		case QueryService:
			out = append(out,
				/* n1ql */ 8093,
				/* n1qlSSL */ 18093,
			)
		case SearchService:
			out = append(out,
				/* fts */ 8094,
				/* ftsGRPC */ 9130,
				/* ftsGRPCSSL */ 19130,
				/* ftsSSL */ 18094,
			)
		case EventingService:
			out = append(out,
				/* eventingAdminPort */ 8096,
				/* eventingDebug */ 9140,
				/* eventingSSL */ 18096,
			)
		case AnalyticsService:
			out = append(out,
				/* cbas */ 8095,
				/* cbasSSL */ 18095,
			)
		case BackupService:
			out = append(out,
				/* backupAPI */ 8097,
				/* backupAPIHTTPS */ 18097,
				/* backupGRPC */ 9124,
			)
		}
	}
	return out, nil
}
