package cbdcconfig

const (
	DEFAULT_AWS_REGION       = "us-west-2"
	DEFAULT_AZURE_REGION     = "westus2"
	DEFAULT_GCP_REGION       = "us-west1"
	DEFAULT_CAPELLA_ENDPOINT = "https://api.cloud.couchbase.com"
	// The v4 API is on a different host from the v2 API and accepts only API keys.
	DEFAULT_CAPELLA_V4_ENDPOINT = "https://cloudapi.cloud.couchbase.com"
	DEFAULT_CAPELLA_PROVIDER    = "aws"
)
