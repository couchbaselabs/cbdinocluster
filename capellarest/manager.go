package capellarest

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Manager struct {
	Logger *zap.Logger
	Client *Client
}

func (m *Manager) WaitForClusterState(
	ctx context.Context,
	tenantID, clusterID string,
	desiredState string,
) error {
	MISSING_STATE := "*MISSING*"

	if desiredState == "" {
		// a blank desired state means to wait until it's deleted...
		desiredState = MISSING_STATE
	}

	for {
		clusters, err := m.Client.ListAllClusters(ctx, tenantID, &PaginatedRequest{
			Page:          1,
			PerPage:       100,
			SortBy:        "name",
			SortDirection: "asc",
		})
		if err != nil {
			return errors.Wrap(err, "failed to list clusters")
		}

		clusterStatus := ""
		for _, cluster := range clusters.Data {
			if cluster.Data.Id == clusterID {
				clusterStatus = cluster.Data.Status.State
			}
		}

		if clusterStatus == "" {
			clusterStatus = MISSING_STATE
		}

		if clusterStatus == MISSING_STATE && desiredState != MISSING_STATE {
			return fmt.Errorf("cluster disappeared during wait for '%s' state", desiredState)
		}

		if clusterStatus != desiredState {
			m.Logger.Info("waiting for cluster status...",
				zap.String("current", clusterStatus),
				zap.String("desired", desiredState))

			time.Sleep(10 * time.Second)
			continue
		}

		break
	}

	return nil
}

func (m *Manager) WaitForPrivateEndpointsEnabled(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) error {
	desiredState := "enabled"

	for {
		pe, err := m.Client.GetPrivateEndpoint(ctx, tenantID, projectID, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to list private endpoint links")
		}

		if pe.Data.Status != desiredState {
			m.Logger.Info("waiting for private endpoints to enable...",
				zap.String("currentState", pe.Data.Status),
				zap.String("desiredState", desiredState))

			time.Sleep(10 * time.Second)
			continue
		}

		return nil
	}
}

func (m *Manager) WaitForPrivateEndpointLink(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	vpceID string,
) (*PrivateEndpointLinkInfo, error) {
	for {
		peLinks, err := m.Client.ListPrivateEndpointLinks(ctx, tenantID, projectID, clusterID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to list private endpoint links")
		}

		var foundLink *PrivateEndpointLinkInfo
		for _, link := range peLinks.Data {
			if link.EndpointID == vpceID {
				foundLink = link
			}
		}

		if foundLink == nil {
			m.Logger.Info("waiting for private endpoint link...",
				zap.String("vpce-id", vpceID))

			time.Sleep(1 * time.Second)
			continue
		}

		return foundLink, nil
	}
}

func (m *Manager) WaitForPrivateEndpointLinkState(
	ctx context.Context,
	tenantID, projectID string, clusterID string,
	vpceID string,
	desiredState string,
) error {
	MISSING_STATE := "*MISSING*"

	if desiredState == "" {
		// a blank desired state means to wait until it's deleted...
		desiredState = MISSING_STATE
	}

	for {
		links, err := m.Client.ListPrivateEndpointLinks(ctx, tenantID, projectID, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to list clusters")
		}

		linkStatus := ""
		for _, link := range links.Data {
			if link.EndpointID == vpceID {
				linkStatus = link.Status
			}
		}

		if linkStatus == "" {
			linkStatus = MISSING_STATE
		}

		if desiredState == "rejected" && linkStatus == MISSING_STATE {
			// if the link gets removed when we are waiting for "rejected", we
			// consider this the same thing since capella removes rejected links
			// after a little while
			linkStatus = "rejected"
		}

		if linkStatus == MISSING_STATE && desiredState != MISSING_STATE {
			return fmt.Errorf("link disappeared during wait for '%s' state", desiredState)
		}

		if linkStatus != desiredState {
			m.Logger.Info("waiting for private endpoint link status...",
				zap.String("current", linkStatus),
				zap.String("desired", desiredState))

			time.Sleep(5 * time.Second)
			continue
		}

		break
	}

	return nil
}
