package clouddeploy

import (
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/stretchr/testify/assert"
)

func TestRemoveAllShouldTake(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour)
	live := now.Add(time.Hour)

	tests := []struct {
		name string
		meta stringclustermeta.MetaData
		opts deployment.RemoveAllOptions
		want bool
	}{
		{
			name: "no scope takes everything",
			meta: stringclustermeta.MetaData{Purpose: "sdk-nightly", Expiry: live},
			want: true,
		},
		{
			name: "no scope takes the never expiring",
			meta: stringclustermeta.MetaData{},
			want: true,
		},
		{
			name: "purpose prefix matches",
			meta: stringclustermeta.MetaData{Purpose: "sdk-nightly"},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk"},
			want: true,
		},
		{
			name: "purpose matches itself",
			meta: stringclustermeta.MetaData{Purpose: "sdk"},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk"},
			want: true,
		},
		{
			name: "purpose mismatch is kept",
			meta: stringclustermeta.MetaData{Purpose: "perf"},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk"},
			want: false,
		},
		{
			name: "empty purpose never matches a prefix",
			meta: stringclustermeta.MetaData{},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk"},
			want: false,
		},
		{
			name: "expired only takes the expired",
			meta: stringclustermeta.MetaData{Expiry: expired},
			opts: deployment.RemoveAllOptions{ExpiredOnly: true},
			want: true,
		},
		{
			name: "expiry equal to now counts as expired",
			meta: stringclustermeta.MetaData{Expiry: now},
			opts: deployment.RemoveAllOptions{ExpiredOnly: true},
			want: true,
		},
		{
			name: "expired only keeps the live",
			meta: stringclustermeta.MetaData{Expiry: live},
			opts: deployment.RemoveAllOptions{ExpiredOnly: true},
			want: false,
		},
		{
			// A zero expiry means the cluster never expires, see Cleanup.
			name: "expired only keeps the never expiring",
			meta: stringclustermeta.MetaData{},
			opts: deployment.RemoveAllOptions{ExpiredOnly: true},
			want: false,
		},
		{
			name: "both filters must match, live is kept",
			meta: stringclustermeta.MetaData{Purpose: "sdk", Expiry: live},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk", ExpiredOnly: true},
			want: false,
		},
		{
			name: "both filters must match, wrong purpose is kept",
			meta: stringclustermeta.MetaData{Purpose: "perf", Expiry: expired},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk", ExpiredOnly: true},
			want: false,
		},
		{
			name: "both filters match",
			meta: stringclustermeta.MetaData{Purpose: "sdk", Expiry: expired},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk", ExpiredOnly: true},
			want: true,
		},
		{
			// A dry run only changes the action, never the scope.
			name: "dry run does not change the scope",
			meta: stringclustermeta.MetaData{Purpose: "perf"},
			opts: deployment.RemoveAllOptions{PurposePrefix: "sdk", DryRun: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, removeAllShouldTake(&tt.meta, tt.opts, now))
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
