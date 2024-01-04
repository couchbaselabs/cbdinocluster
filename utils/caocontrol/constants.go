package caocontrol

const (
	GhcrK8sSecretName      = "ghcr-secret"
	CbdcCbcAdminSecretName = "cbdc-example-auth"
	CNGTLSSecretNamePrefix = "-cloud-native-gateway-tls"
	CNGServiceNamePrefix   = "-cloud-native-gateway-service"
)

const (
	AdmissionResourceName = "couchbase-operator-admission"
	OperatorResourceName  = "couchbase-operator"
	OperatorImageName     = "couchbase/operator"
	AdmissionImageName    = "couchbase/admission-controller"
	OpenshiftRouteName    = "grpc-route"
)
