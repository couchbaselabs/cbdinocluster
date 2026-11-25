package clouddeploy

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
)

func testMeta(purpose string) stringclustermeta.MetaData {
	return stringclustermeta.MetaData{
		ID:      cbdcuuid.New(),
		Expiry:  time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 2*60*60)),
		Purpose: purpose,
	}
}

func parseMetaName(t *testing.T, name string) *stringclustermeta.MetaData {
	t.Helper()
	parsed, err := stringclustermeta.Parse(name)
	require.NoError(t, err)
	require.NotNil(t, parsed)
	return parsed
}

func TestMetaNameForShortPurpose(t *testing.T) {
	for _, purpose := range []string{"my test", "a_b-c__d_-e"} {
		meta := testMeta(purpose)

		name, gotPurpose, trimmed := metaNameFor(meta, maxClusterNameLen)
		assert.False(t, trimmed)
		assert.Equal(t, purpose, gotPurpose)

		parsed := parseMetaName(t, name)
		assert.Equal(t, meta.ID, parsed.ID)
		assert.Equal(t, meta.Expiry.UTC(), parsed.Expiry)
		assert.Equal(t, purpose, parsed.Purpose)
	}
}

func TestMetaNameForLongPurpose(t *testing.T) {
	for _, maxLen := range []int{maxProjectNameLen, maxClusterNameLen} {
		meta := testMeta(strings.Repeat("p_-", 200))

		name, purpose, trimmed := metaNameFor(meta, maxLen)
		assert.True(t, trimmed)
		assert.Len(t, name, maxLen)

		parsed := parseMetaName(t, name)
		assert.Equal(t, meta.ID, parsed.ID)
		assert.Equal(t, meta.Expiry.UTC(), parsed.Expiry)
		assert.Equal(t, purpose, parsed.Purpose)
		assert.NotEmpty(t, parsed.Purpose)
		assert.True(t, strings.HasPrefix(meta.Purpose, parsed.Purpose))
	}
}

func TestMetaNameForMultiBytePurpose(t *testing.T) {
	for _, maxLen := range []int{maxProjectNameLen, maxClusterNameLen} {
		// The name prefix is 49 bytes, so the cut lands inside a 2 byte rune.
		meta := testMeta(strings.Repeat("é", 200))

		name, _, trimmed := metaNameFor(meta, maxLen)
		assert.True(t, trimmed)
		assert.Len(t, name, maxLen-1)
		assert.True(t, utf8.ValidString(name))

		parsed := parseMetaName(t, name)
		assert.True(t, strings.HasPrefix(meta.Purpose, parsed.Purpose))
	}
}

func TestMetaNameForNoPurposeZeroExpiry(t *testing.T) {
	meta := stringclustermeta.MetaData{ID: cbdcuuid.New()}

	name, _, trimmed := metaNameFor(meta, maxProjectNameLen)
	assert.False(t, trimmed)

	parsed := parseMetaName(t, name)
	assert.Equal(t, meta.ID, parsed.ID)
	assert.True(t, parsed.Expiry.IsZero())
	assert.Empty(t, parsed.Purpose)
}

func TestOldLayoutClusterNameHasNoMeta(t *testing.T) {
	name := fmt.Sprintf("cbdc2_%s", cbdcuuid.New())

	parsed, err := stringclustermeta.Parse(name)
	assert.NoError(t, err)
	assert.Nil(t, parsed)
}
