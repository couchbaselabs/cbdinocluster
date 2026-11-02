package capellav4

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func statusSequence(statuses ...string) (func(ctx context.Context) (string, error), *int) {
	calls := 0
	return func(ctx context.Context) (string, error) {
		status := statuses[min(calls, len(statuses)-1)]
		calls++
		return status, nil
	}, &calls
}

func TestWaitForPrivateEndpointServiceEnabledRidesOutEnableFailed(t *testing.T) {
	m := &Manager{Logger: zap.NewNop()}
	fetch, calls := statusSequence("enabling", "enableFailed", "enableFailed", "enabling", "enabled")

	err := m.waitForPrivateEndpointServiceEnabled(context.Background(), fetch, time.Millisecond, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 5, *calls)
}

func TestWaitForPrivateEndpointServiceEnabledGivesUpAfterGrace(t *testing.T) {
	m := &Manager{Logger: zap.NewNop()}
	fetch, _ := statusSequence("enabling", "enableFailed")

	err := m.waitForPrivateEndpointServiceEnabled(context.Background(), fetch, time.Millisecond, 20*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stayed in a failed state ('enableFailed')")
}

func TestWaitForPrivateEndpointServiceEnabledGraceResetsWhenStatusRecovers(t *testing.T) {
	m := &Manager{Logger: zap.NewNop()}
	// Each enableFailed stretch is shorter than the grace, but together they exceed it.
	statuses := []string{}
	for i := 0; i < 4; i++ {
		for j := 0; j < 10; j++ {
			statuses = append(statuses, "enableFailed")
		}
		statuses = append(statuses, "enabling")
	}
	statuses = append(statuses, "enabled")
	fetch, _ := statusSequence(statuses...)

	err := m.waitForPrivateEndpointServiceEnabled(context.Background(), fetch, 5*time.Millisecond, 150*time.Millisecond)
	require.NoError(t, err)
}

func TestWaitForPrivateEndpointServiceEnabledFailsFastOnOtherFailures(t *testing.T) {
	m := &Manager{Logger: zap.NewNop()}
	fetch, calls := statusSequence("disableFailed", "enabled")

	err := m.waitForPrivateEndpointServiceEnabled(context.Background(), fetch, time.Millisecond, time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disableFailed")
	assert.Equal(t, 1, *calls)
}

func TestWaitForPrivateEndpointServiceEnabledReturnsFetchErrors(t *testing.T) {
	m := &Manager{Logger: zap.NewNop()}
	fetch := func(ctx context.Context) (string, error) { return "", errors.New("boom") }

	err := m.waitForPrivateEndpointServiceEnabled(context.Background(), fetch, time.Millisecond, time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}
