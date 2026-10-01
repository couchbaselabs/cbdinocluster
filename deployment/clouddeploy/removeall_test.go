package clouddeploy

import (
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/stretchr/testify/assert"
)

func TestCleanupShouldTake(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour)
	live := now.Add(time.Hour)

	tests := []struct {
		name string
		meta stringclustermeta.MetaData
		opts deployment.CleanupOptions
		want bool
	}{
		{
			name: "takes the expired",
			meta: stringclustermeta.MetaData{Expiry: expired},
			want: true,
		},
		{
			name: "expiry equal to now counts as expired",
			meta: stringclustermeta.MetaData{Expiry: now},
			want: true,
		},
		{
			name: "keeps the live",
			meta: stringclustermeta.MetaData{Expiry: live},
			want: false,
		},
		{
			name: "keeps the never expiring",
			meta: stringclustermeta.MetaData{},
			want: false,
		},
		{
			name: "purpose scope takes the expired match",
			meta: stringclustermeta.MetaData{Purpose: "sdk-nightly", Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: true,
		},
		{
			name: "purpose scope keeps the live match",
			meta: stringclustermeta.MetaData{Purpose: "sdk", Expiry: live},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: false,
		},
		{
			name: "purpose scope keeps the expired mismatch",
			meta: stringclustermeta.MetaData{Purpose: "sdkxyz", Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: false,
		},
		{
			name: "purpose scope keeps an expired empty purpose",
			meta: stringclustermeta.MetaData{Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: false,
		},
		{
			name: "dry run does not change the scope",
			meta: stringclustermeta.MetaData{Purpose: "perf", Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk", DryRun: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cleanupShouldTake(&tt.meta, tt.opts, now))
		})
	}
}

func TestShouldSkipDestroyFailed(t *testing.T) {
	tests := []struct {
		name  string
		state string
		skip  bool
		want  bool
	}{
		{name: "cleanup skips destroyFailed", state: capellav4.StateDestroyFailed, skip: true, want: true},
		{name: "cleanup takes healthy", state: capellav4.StateHealthy, skip: true, want: false},
		{name: "cleanup takes destroying", state: capellav4.StateDestroying, skip: true, want: false},
		{name: "cleanup takes an unknown state", state: "", skip: true, want: false},
		{name: "remove-all takes destroyFailed", state: capellav4.StateDestroyFailed, skip: false, want: false},
		{name: "remove-all takes healthy", state: capellav4.StateHealthy, skip: false, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldSkipDestroyFailed(tt.state, tt.skip))
		})
	}
}

// Every project delete goes through the ownership guard, so it must accept
// every name cbdinocluster generates and refuse everything else.
func TestCanDeleteProjectName(t *testing.T) {
	ownedMeta := stringclustermeta.MetaData{
		ID:     cbdcuuid.New(),
		Expiry: time.Now().Add(time.Hour),
	}
	assert.True(t, canDeleteProjectName(ownedMeta.String()))

	ownedMeta.Purpose = "sdk-nightly"
	assert.True(t, canDeleteProjectName(ownedMeta.String()))

	assert.False(t, canDeleteProjectName(""))
	assert.False(t, canDeleteProjectName("shared-team-project"))
	assert.False(t, canDeleteProjectName("cbdc2_notauuid_20260101-000000"))
	assert.False(t, canDeleteProjectName("cbdc2_onlyoneextrapart"))
}
