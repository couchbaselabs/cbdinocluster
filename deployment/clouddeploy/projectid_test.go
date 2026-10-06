package clouddeploy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
)

func TestCheckProjectID(t *testing.T) {
	valid := []string{
		"0b1c2d3e-4f5a-6b7c-8d9e-0f1a2b3c4d5e",
		"0B1C2D3E-4F5A-6B7C-8D9E-0F1A2B3C4D5E",
	}
	for _, id := range valid {
		assert.NoError(t, CheckProjectID(id), "id %q", id)
	}

	invalid := []string{
		"cbdinocluster-shared",
		"cbdc2_0123456789abcdef_20261005-120000",
		"0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e",
		"{0b1c2d3e-4f5a-6b7c-8d9e-0f1a2b3c4d5e}",
		" 0b1c2d3e-4f5a-6b7c-8d9e-0f1a2b3c4d5e",
		"0b1c2d3e-4f5a-6b7c-8d9e-0f1a2b3c4d5g",
	}
	for _, id := range invalid {
		assert.Error(t, CheckProjectID(id), "id %q", id)
	}
}

func TestNewDeployerChecksProjectID(t *testing.T) {
	client, err := capellav4.NewClient(&capellav4.ClientOptions{
		Endpoint:   "http://127.0.0.1:0",
		SecretKeys: []string{"test-secret"},
	})
	require.NoError(t, err)

	for _, id := range []string{"", "0b1c2d3e-4f5a-6b7c-8d9e-0f1a2b3c4d5e"} {
		deployer, err := NewDeployer(&NewDeployerOptions{V4Client: client, ProjectID: id})
		require.NoError(t, err, "id %q", id)
		assert.Equal(t, id, deployer.projectID)
	}

	_, err = NewDeployer(&NewDeployerOptions{V4Client: client, ProjectID: "cbdinocluster-shared"})
	require.ErrorContains(t, err, "is not a UUID")
}
