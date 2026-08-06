package capellav4

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// Manager adds polling helpers on top of Client.
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

// WaitForClusterState polls a single cluster until it reaches desiredState.
// Passing StateDeleted waits for the cluster to disappear instead.
//
// This polls the cluster directly rather than listing, which matters because the
// v4 API has no organization-wide cluster list and listing would cost one request
// per project on every poll.
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

		if desiredState == StateDeleted {
			m.Logger.Info("waiting for cluster deletion...",
				zap.String("current", currentState))

			if err := m.sleep(ctx, clusterPollInterval); err != nil {
				return err
			}
			continue
		}

		// Capella reports terminal failures as deployment_failed,
		// destroy_failed, scale_failed and similar, so match on the suffix
		// rather than listing every variant.
		if strings.Contains(currentState, "failed") && currentState != desiredState {
			return fmt.Errorf("cancelling as cluster is in a failed state ('%s')", currentState)
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

// WaitForDataApiEnabled polls until the Data API finishes enabling.
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

// WaitForPrivateEndpointServiceEnabled polls until the private endpoint service
// is ready to accept endpoint requests.
func (m *Manager) WaitForPrivateEndpointServiceEnabled(ctx context.Context, orgID, projectID, clusterID string) error {
	return m.waitForPrivateEndpointServiceEnabled(ctx, func(ctx context.Context) (string, error) {
		info, err := m.Client.GetPrivateEndpointService(ctx, orgID, projectID, clusterID)
		if err != nil {
			return "", err
		}
		return info.Status, nil
	})
}

// WaitForAnalyticsPrivateEndpointServiceEnabled is the analytics cluster form of
// WaitForPrivateEndpointServiceEnabled.
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

		if status == PrivateEndpointServiceEnabled {
			return nil
		}

		if err := m.sleep(ctx, clusterPollInterval); err != nil {
			return err
		}
	}
}

// privateEndpointLister abstracts over the provisioned and analytics endpoint
// listings, which return the same information under different response shapes.
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

// WaitForPrivateEndpoint polls until an endpoint with the given ID is visible to
// Capella. The endpoint only appears once the cloud provider has propagated the
// connection request.
func (m *Manager) WaitForPrivateEndpoint(
	ctx context.Context,
	orgID, projectID, clusterID string,
	endpointID string,
) (*PrivateEndpointInfo, error) {
	return m.waitForPrivateEndpoint(ctx, m.listClusterPrivateEndpoints(orgID, projectID, clusterID), endpointID)
}

// WaitForAnalyticsPrivateEndpoint is the analytics cluster form of
// WaitForPrivateEndpoint.
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

// WaitForPrivateEndpointState polls until an endpoint reaches desiredState.
func (m *Manager) WaitForPrivateEndpointState(
	ctx context.Context,
	orgID, projectID, clusterID string,
	endpointID string,
	desiredState string,
) error {
	return m.waitForPrivateEndpointState(ctx, m.listClusterPrivateEndpoints(orgID, projectID, clusterID), endpointID, desiredState)
}

// WaitForAnalyticsPrivateEndpointState is the analytics cluster form of
// WaitForPrivateEndpointState.
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

		// Capella drops rejected endpoints after a while, so a missing endpoint
		// satisfies a wait for the rejected state.
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
