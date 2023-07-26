package cbmgmtrest

type NodeSetupOptions struct {
	EnableKvService       bool
	EnableN1qlService     bool
	EnableIndexService    bool
	EnableFtsService      bool
	EnableCbasService     bool
	EnableEventingService bool
	EnableBackupService   bool
}

func (o NodeSetupOptions) ServicesList() []string {
	var serviceNames []string
	if o.EnableKvService {
		serviceNames = append(serviceNames, "kv")
	}
	if o.EnableN1qlService {
		serviceNames = append(serviceNames, "query")
	}
	if o.EnableIndexService {
		serviceNames = append(serviceNames, "index")
	}
	if o.EnableFtsService {
		serviceNames = append(serviceNames, "fts")
	}
	if o.EnableCbasService {
		serviceNames = append(serviceNames, "cbas")
	}
	if o.EnableEventingService {
		serviceNames = append(serviceNames, "eventing")
	}
	if o.EnableBackupService {
		serviceNames = append(serviceNames, "backup")
	}
	return serviceNames
}

func (o NodeSetupOptions) NsServicesList() []string {
	var serviceNames []string
	if o.EnableKvService {
		serviceNames = append(serviceNames, "kv")
	}
	if o.EnableN1qlService {
		serviceNames = append(serviceNames, "n1ql")
	}
	if o.EnableIndexService {
		serviceNames = append(serviceNames, "index")
	}
	if o.EnableFtsService {
		serviceNames = append(serviceNames, "fts")
	}
	if o.EnableCbasService {
		serviceNames = append(serviceNames, "cbas")
	}
	if o.EnableEventingService {
		serviceNames = append(serviceNames, "eventing")
	}
	if o.EnableBackupService {
		serviceNames = append(serviceNames, "backup")
	}
	return serviceNames
}
