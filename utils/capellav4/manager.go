package capellav4

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
	Client *Client
}

const (
	clusterPollInterval  = 10 * time.Second
	endpointPollInterval = 5 * time.Second
)

// StateDeleted is the desired state that means "wait until the resource is gone".
const StateDeleted = ""

func (m *Manager) sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (m *Manager) WaitForClusterState(
	ctx context.Context,
	orgID, projectID, clusterID string,
	desiredState string,
) error {
	for {
		cluster, err := m.Client.GetCluster(ctx, orgID, projectID, clusterID)

		currentState := ""
		switch {
		case IsNotFound(err):
			if desiredState == StateDeleted {
				return nil
			}
			return fmt.Errorf("cluster disappeared during wait for '%s' state", desiredState)
		case err != nil:
			return errors.Wrap(err, "failed to fetch cluster")
		default:
			currentState = cluster.CurrentState
		}

		// Terminal failure states share a "Failed" suffix, such as deploymentFailed.
		if strings.HasSuffix(currentState, "Failed") && currentState != desiredState {
			return fmt.Errorf("cancelling as cluster is in a failed state ('%s')", currentState)
		}

		if desiredState == StateDeleted {
			m.Logger.Info("waiting for cluster deletion...",
				zap.String("current", currentState))

			if err := m.sleep(ctx, clusterPollInterval); err != nil {
				return err
			}
			continue
		}

		m.Logger.Info("waiting for cluster status...",
			zap.String("current", currentState),
			zap.String("desired", desiredState))

		if currentState == desiredState {
			return nil
		}

		if err := m.sleep(ctx, clusterPollInterval); err != nil {
			return err
		}
	}
}

// The v4 API has no single analytics cluster fetch, so the project is listed.
func (m *Manager) WaitForAnalyticsClusterState(
	ctx context.Context,
	orgID, projectID, clusterID string,
	desiredState string,
) error {
	for {
		clusters, err := m.Client.ListAnalyticsClusters(ctx, orgID, projectID)
		if err != nil {
			return errors.Wrap(err, "failed to list analytics clusters")
		}

		currentState := ""
		found := false
		for _, cluster := range clusters {
			if cluster.ID == clusterID {
				currentState = cluster.CurrentState
				found = true
			}
		}

		if !found {
			if desiredState == StateDeleted {
				return nil
			}
			return fmt.Errorf("analytics cluster disappeared during wait for '%s' state", desiredState)
		}

		// Terminal failure states share a "Failed" suffix, such as deploymentFailed.
		if strings.HasSuffix(currentState, "Failed") && currentState != desiredState {
			return fmt.Errorf("cancelling as cluster is in a failed state ('%s')", currentState)
		}

		if desiredState == StateDeleted {
			m.Logger.Info("waiting for cluster deletion...",
				zap.String("current", currentState))

			if err := m.sleep(ctx, clusterPollInterval); err != nil {
				return err
			}
			continue
		}

		m.Logger.Info("waiting for cluster status...",
			zap.String("current", currentState),
			zap.String("desired", desiredState))

		if currentState == desiredState {
			return nil
		}

		if err := m.sleep(ctx, clusterPollInterval); err != nil {
			return err
		}
	}
}

func (m *Manager) WaitForDataApiEnabled(ctx context.Context, orgID, projectID, clusterID string) error {
	for {
		info, err := m.Client.GetDataApi(ctx, orgID, projectID, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to fetch data api state")
		}

		m.Logger.Info("waiting for data api state...",
			zap.String("current", info.State),
			zap.String("desired", DataApiStateEnabled))

		if info.State == DataApiStateEnabled {
			return nil
		}

		if err := m.sleep(ctx, clusterPollInterval); err != nil {
			return err
		}
	}
}

func (m *Manager) WaitForPrivateEndpointServiceEnabled(ctx context.Context, orgID, projectID, clusterID string) error {
	return m.waitForPrivateEndpointServiceEnabled(ctx, func(ctx context.Context) (string, error) {
		info, err := m.Client.GetPrivateEndpointService(ctx, orgID, projectID, clusterID)
		if err != nil {
			return "", err
		}
		return info.Status, nil
	})
}

func (m *Manager) WaitForAnalyticsPrivateEndpointServiceEnabled(ctx context.Context, orgID, projectID, clusterID string) error {
	return m.waitForPrivateEndpointServiceEnabled(ctx, func(ctx context.Context) (string, error) {
		info, err := m.Client.GetAnalyticsPrivateEndpointService(ctx, orgID, projectID, clusterID)
		if err != nil {
			return "", err
		}
		return info.Status, nil
	})
}

func (m *Manager) waitForPrivateEndpointServiceEnabled(
	ctx context.Context,
	fetchStatus func(ctx context.Context) (string, error),
) error {
	for {
		status, err := fetchStatus(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch private endpoint service")
		}

		m.Logger.Info("waiting for private endpoints to enable...",
			zap.String("current", status),
			zap.String("desired", PrivateEndpointServiceEnabled))

		// Terminal failure states share a "Failed" suffix, such as enableFailed.
		if strings.HasSuffix(status, "Failed") {
			return fmt.Errorf("cancelling as private endpoint service is in a failed state ('%s')", status)
		}

		if status == PrivateEndpointServiceEnabled {
			return nil
		}

		if err := m.sleep(ctx, clusterPollInterval); err != nil {
			return err
		}
	}
}

type privateEndpointLister func(ctx context.Context) ([]*PrivateEndpointInfo, error)

func (m *Manager) listClusterPrivateEndpoints(orgID, projectID, clusterID string) privateEndpointLister {
	return func(ctx context.Context) ([]*PrivateEndpointInfo, error) {
		resp, err := m.Client.ListPrivateEndpoints(ctx, orgID, projectID, clusterID)
		if err != nil {
			return nil, err
		}
		return resp.Endpoints, nil
	}
}

func (m *Manager) listAnalyticsPrivateEndpoints(orgID, projectID, clusterID string) privateEndpointLister {
	return func(ctx context.Context) ([]*PrivateEndpointInfo, error) {
		return m.Client.ListAnalyticsPrivateEndpoints(ctx, orgID, projectID, clusterID)
	}
}

// The endpoint appears only after the cloud provider propagates the request.
func (m *Manager) WaitForPrivateEndpoint(
	ctx context.Context,
	orgID, projectID, clusterID string,
	endpointID string,
) (*PrivateEndpointInfo, error) {
	return m.waitForPrivateEndpoint(ctx, m.listClusterPrivateEndpoints(orgID, projectID, clusterID), endpointID)
}

func (m *Manager) WaitForAnalyticsPrivateEndpoint(
	ctx context.Context,
	orgID, projectID, clusterID string,
	endpointID string,
) (*PrivateEndpointInfo, error) {
	return m.waitForPrivateEndpoint(ctx, m.listAnalyticsPrivateEndpoints(orgID, projectID, clusterID), endpointID)
}

func (m *Manager) waitForPrivateEndpoint(
	ctx context.Context,
	list privateEndpointLister,
	endpointID string,
) (*PrivateEndpointInfo, error) {
	for {
		endpoints, err := list(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to list private endpoints")
		}

		for _, endpoint := range endpoints {
			if endpoint.ID == endpointID {
				m.Logger.Info("found!", zap.String("endpoint-id", endpointID))
				return endpoint, nil
			}
		}

		m.Logger.Info("waiting for private endpoint...",
			zap.String("endpoint-id", endpointID))

		if err := m.sleep(ctx, endpointPollInterval); err != nil {
			return nil, err
		}
	}
}

func (m *Manager) WaitForPrivateEndpointState(
	ctx context.Context,
	orgID, projectID, clusterID string,
	endpointID string,
	desiredState string,
) error {
	return m.waitForPrivateEndpointState(ctx, m.listClusterPrivateEndpoints(orgID, projectID, clusterID), endpointID, desiredState)
}

func (m *Manager) WaitForAnalyticsPrivateEndpointState(
	ctx context.Context,
	orgID, projectID, clusterID string,
	endpointID string,
	desiredState string,
) error {
	return m.waitForPrivateEndpointState(ctx, m.listAnalyticsPrivateEndpoints(orgID, projectID, clusterID), endpointID, desiredState)
}

func (m *Manager) waitForPrivateEndpointState(
	ctx context.Context,
	list privateEndpointLister,
	endpointID string,
	desiredState string,
) error {
	for {
		endpoints, err := list(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to list private endpoints")
		}

		currentState := ""
		for _, endpoint := range endpoints {
			if endpoint.ID == endpointID {
				currentState = endpoint.Status
			}
		}

		// Capella drops rejected endpoints, so a missing endpoint is a rejected one.
		if currentState == "" && desiredState == PrivateEndpointRejected {
			return nil
		}

		if currentState == "" && desiredState != StateDeleted {
			return fmt.Errorf("endpoint disappeared during wait for '%s' state", desiredState)
		}

		m.Logger.Info("waiting for private endpoint status...",
			zap.String("current", currentState),
			zap.String("desired", desiredState))

		if currentState == desiredState {
			return nil
		}

		if err := m.sleep(ctx, endpointPollInterval); err != nil {
			return err
		}
	}
}
