package cbdcconfig

const (
	DEFAULT_AWS_REGION       = "us-west-2"
	DEFAULT_AZURE_REGION     = "westus2"
	DEFAULT_GCP_REGION       = "us-west1"
	DEFAULT_CAPELLA_ENDPOINT = "https://api.cloud.couchbase.com"
	DEFAULT_CAPELLA_PROVIDER = "aws"
)

const (
	// ONLY, released(public) operator/admission controller images are used here
	// This is because we are NOT testing on unreleased(private/ghcr) operator/admission controlle
	// We use CNG w/ operator from version 2.5.0+
	DEFAULT_CAO_OPERATOR_VERSION	= "2.5.0"
	// We use CNG w/ admission from version 2.5.0+
	DEFAULT_CAO_ADMISSION_VERSION	= "2.5.0"
	// We use CNG w/ admission from version 2.5.0+
	DEFAULT_CAO_CRD_FILE_PATH		= "cbdcconfig/cao/2.5/crd.yaml"
	// cao binary doesn't chnage usually, unlesss a new command/flag need to be used.
	DEFAULT_CAO_BIN_PATH			= "./cbdcconfig/cao/bin/cao"

	DEFAULT_NAMESPACE 				= "default"
)
