package clusterdef

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
