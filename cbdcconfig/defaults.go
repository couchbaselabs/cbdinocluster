package cbdcconfig

const (
	DEFAULT_AWS_REGION       = "us-west-2"
	DEFAULT_AZURE_REGION     = "westus2"
	DEFAULT_GCP_REGION       = "us-west1"
	DEFAULT_CAPELLA_ENDPOINT = "https://api.cloud.couchbase.com"
	// DEFAULT_CAPELLA_V4_ENDPOINT is the public Management API v4 host. It is a
	// different host from the internal v2 API and only accepts API key
	// credentials.
	DEFAULT_CAPELLA_V4_ENDPOINT = "https://cloudapi.cloud.couchbase.com"
	DEFAULT_CAPELLA_PROVIDER    = "aws"
)
