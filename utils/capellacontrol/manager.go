package capellacontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Manager struct {
	Logger *zap.Logger
	Client *Controller
}

func (m *Manager) WaitForClusterState(
	ctx context.Context,
	tenantID, clusterID string,
	desiredState string,
	columnar bool,
) error {
	MISSING_STATE := "*MISSING*"

	if desiredState == "" {
		// a blank desired state means to wait until it's deleted...
		desiredState = MISSING_STATE
	}

	for {
		clusterStatus := ""
		if !columnar {
			clusters, err := m.Client.ListAllClusters(ctx, tenantID, &PaginatedRequest{
				Page:          1,
				PerPage:       100,
				SortBy:        "name",
				SortDirection: "asc",
			})
			if err != nil {
				return errors.Wrap(err, "failed to list clusters")
			}

			for _, cluster := range clusters.Data {
				if cluster.Data.Id == clusterID {
					clusterStatus = cluster.Data.Status.State
				}
			}
		} else {
			columnars, err := m.Client.ListAllColumnars(ctx, tenantID, &PaginatedRequest{
				Page:          1,
				PerPage:       100,
				SortBy:        "name",
				SortDirection: "asc",
			})
			if err != nil {
				return errors.Wrap(err, "failed to list columnars")
			}

			for _, columnar := range columnars.Data {
				if columnar.Data.ID == clusterID {
					clusterStatus = columnar.Data.State
				}
			}
		}

		if clusterStatus == "" {
			clusterStatus = MISSING_STATE
		}

		if clusterStatus == MISSING_STATE && desiredState != MISSING_STATE {
			return fmt.Errorf("cluster disappeared during wait for '%s' state", desiredState)
		}

		if strings.Contains(clusterStatus, "failed") {
			return fmt.Errorf("cancelling as cluster is in a failed state ('%s')", clusterStatus)
		}

		m.Logger.Info("waiting for cluster status...",
			zap.String("current", clusterStatus),
			zap.String("desired", desiredState))

		if clusterStatus != desiredState {
			time.Sleep(10 * time.Second)
			continue
		}

		break
	}
	return nil
}

func (m *Manager) WaitForPrivateEndpointsEnabled(
	ctx context.Context,
	columnar bool,
	tenantID, projectID, clusterID string,
) error {
	desiredState := "enabled"

	for {
		var pe *GetPrivateEndpointResponse
		var err error
		if !columnar {
			pe, err = m.Client.GetPrivateEndpoint(ctx, tenantID, projectID, clusterID)
		} else {
			pe, err = m.Client.GetPrivateEndpointColumnar(ctx, tenantID, projectID, clusterID)
		}
		if err != nil {
			return errors.Wrap(err, "failed to list private endpoint links")
		}

		m.Logger.Info("waiting for private endpoints to enable...",
			zap.String("currentState", pe.Data.Status),
			zap.String("desiredState", desiredState))

		if pe.Data.Status != desiredState {
			time.Sleep(10 * time.Second)
			continue
		}

		return nil
	}
}

func (m *Manager) WaitForPrivateEndpointLink(
	ctx context.Context,
	columnar bool,
	tenantID, projectID, clusterID string,
	vpceID string,
) (*PrivateEndpointLinkInfo, error) {
	for {
		var peLinks *ListPrivateEndpointLinksResponse
		var err error
		if !columnar {
			peLinks, err = m.Client.ListPrivateEndpointLinks(ctx, tenantID, projectID, clusterID)
		} else {
			peLinks, err = m.Client.ListPrivateEndpointLinksColumnar(ctx, tenantID, projectID, clusterID)
		}
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

		m.Logger.Info("found!",
			zap.String("vpce-id", vpceID))

		return foundLink, nil
	}
}

func (m *Manager) WaitForPrivateEndpointLinkState(
	ctx context.Context,
	columnar bool,
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
		var links *ListPrivateEndpointLinksResponse
		var err error
		if !columnar {
			links, err = m.Client.ListPrivateEndpointLinks(ctx, tenantID, projectID, clusterID)
		} else {
			links, err = m.Client.ListPrivateEndpointLinksColumnar(ctx, tenantID, projectID, clusterID)
		}
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

		m.Logger.Info("waiting for private endpoint link status...",
			zap.String("current", linkStatus),
			zap.String("desired", desiredState))

		if linkStatus != desiredState {
			time.Sleep(5 * time.Second)
			continue
		}

		break
	}

	return nil
}

func (m *Manager) WaitForServerLogsCollected(
	ctx context.Context,
	clusterID string,
	token string,
	req *DownloadServerLogsRequest,
) (map[string]PerNode, error) {
	desiredState := "completed"

	for {
		resp, err := m.Client.DownloadServerLogs(ctx, clusterID, token, req)
		if err != nil {
			return nil, errors.Wrap(err, "Download server logs request failed")
		}

		var logCollectionStatus *DownloadServerLogsStatus
		for _, status := range resp.DownloadServerLogsStatuses {
			if status.Type == "clusterLogsCollection" {
				logCollectionStatus = &status
				break
			}
		}

		var perNode, status = logCollectionStatus.PerNode, logCollectionStatus.Status

		m.Logger.Info("waiting for logs to be collected...",
			zap.String("currentState", status),
			zap.String("desiredState", desiredState))

		if status != desiredState {
			time.Sleep(15 * time.Second)
			continue
		}

		return perNode, nil
	}
}

func (m *Manager) WaitForDataApiEnabled(
	ctx context.Context,
	tenantID, clusterID string) (string, error) {
	desiredState := "enabled"
	dataApiHostname := ""

	for {
		dataApiState := ""

		clusters, err := m.Client.ListAllClusters(ctx, tenantID, &PaginatedRequest{
			Page:          1,
			PerPage:       100,
			SortBy:        "name",
			SortDirection: "asc",
		})
		if err != nil {
			return "", errors.Wrap(err, "failed to list clusters")
		}

		for _, cluster := range clusters.Data {
			if cluster.Data.Id == clusterID {
				dataApiState = cluster.Data.DataApiState
				dataApiHostname = cluster.Data.DataApiHostname
			}
		}

		if dataApiState == "" {
			return "", fmt.Errorf("cluster disappeared while waiting for data API to be enabled")
		}

		m.Logger.Info("waiting for data api state...",
			zap.String("current", dataApiState),
			zap.String("desired", desiredState))

		if dataApiState != desiredState {
			time.Sleep(10 * time.Second)
			continue
		}

		break
	}

	return dataApiHostname, nil
}
