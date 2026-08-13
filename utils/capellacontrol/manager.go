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

func (m *Manager) WaitForColumnarDeletion(
	ctx context.Context,
	tenantID, instanceID, cloudClusterID string,
) error {
	if err := m.WaitForClusterState(ctx, tenantID, instanceID, "", true); err != nil {
		return err
	}

	req := &PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		events, err := m.Client.GetClusterDeletionEvents(ctx, tenantID, cloudClusterID, req)
		if err != nil {
			return errors.Wrap(err, "failed to fetch cluster deletion events")
		}
		if len(events.Data) > 0 {
			return nil
		}

		m.Logger.Info("waiting for underlying cluster deletion to complete...",
			zap.String("cluster-id", cloudClusterID))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
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
