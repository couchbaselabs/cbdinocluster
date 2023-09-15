package capellacontrol_test

import (
	"context"
	"os"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestMultipleControllers(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	capellaUser := os.Getenv("CAPELLA_USER")
	capellaPass := os.Getenv("CAPELLA_PASS")
	capellaOid := os.Getenv("CAPELLA_OID")

	ctrl1, err := capellacontrol.NewController(ctx, &capellacontrol.ControllerOptions{
		Logger:   logger,
		Endpoint: "https://api.cloud.couchbase.com",
		Auth: &capellacontrol.BasicCredentials{
			Username: capellaUser,
			Password: capellaPass,
		},
	})
	require.NoError(t, err)

	ctrl2, err := capellacontrol.NewController(ctx, &capellacontrol.ControllerOptions{
		Logger:   logger,
		Endpoint: "https://api.cloud.couchbase.com",
		Auth: &capellacontrol.BasicCredentials{
			Username: capellaUser,
			Password: capellaPass,
		},
	})
	require.NoError(t, err)

	_, err = ctrl1.ListProjects(ctx, capellaOid, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	require.NoError(t, err)

	_, err = ctrl2.ListProjects(ctx, capellaOid, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	require.NoError(t, err)

	_, err = ctrl1.ListProjects(ctx, capellaOid, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	require.NoError(t, err)
}
