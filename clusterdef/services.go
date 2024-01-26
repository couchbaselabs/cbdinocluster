package clusterdef

import (
	"errors"

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
