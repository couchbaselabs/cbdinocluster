package stringclustermeta_test

import (
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/stretchr/testify/require"
)

func TestStringClusterMeta(t *testing.T) {
	testOne := func(md stringclustermeta.MetaData) {
		str := md.String()

		// we encode times relative to UTC, so we need to switch the
		// meta-data to UTC after we've encoded it but before we compare
		// it with the output
		md.Expiry = md.Expiry.UTC()

		mdOut, err := stringclustermeta.Parse(str)
		require.NoError(t, err)
		require.Equal(t, md, *mdOut)
	}

	testUuid := cbdcuuid.UUID([16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 0xa, 0xb, 0xc, 0xd, 0xe, 0xf})
	testExpiry := time.Unix(104039403, 0)

	// Without purpose
	testOne(stringclustermeta.MetaData{
		ID:     testUuid,
		Expiry: testExpiry,
	})

	// With purpose
	testOne(stringclustermeta.MetaData{
		ID:      testUuid,
		Expiry:  testExpiry,
		Purpose: "this is a test",
	})
}
