package versionident_test

import (
	"context"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/versionident"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionIdent(t *testing.T) {
	ctx := context.Background()

	checkVersion := func(input string, expected *versionident.Version) {
		v, err := versionident.Identify(ctx, input)
		if expected == nil {
			require.Error(t, err)
		} else {
			assert.NoError(t, err)
			if v != nil {
				require.Equal(t, expected.Version, v.Version)
				require.Equal(t, expected.BuildNo, v.BuildNo)
				require.Equal(t, expected.CommunityEdition, v.CommunityEdition)
				require.Equal(t, expected.Serverless, v.Serverless)
			}
		}
	}

	checkVersion("7.0.0", &versionident.Version{
		Version:          "7.0.0",
		BuildNo:          0,
		CommunityEdition: false,
		Serverless:       false,
	})
	checkVersion("7.2.0", &versionident.Version{
		Version:          "7.2.0",
		BuildNo:          0,
		CommunityEdition: false,
		Serverless:       false,
	})
	checkVersion("7.2", &versionident.Version{
		Version:          "7.2",
		BuildNo:          0,
		CommunityEdition: false,
		Serverless:       false,
	})
	checkVersion("community-7.2.0", &versionident.Version{
		Version:          "7.2.0",
		BuildNo:          0,
		CommunityEdition: true,
		Serverless:       false,
	})
	checkVersion("7.2.0-14", &versionident.Version{
		Version:          "7.2.0",
		BuildNo:          14,
		CommunityEdition: false,
		Serverless:       false,
	})
	checkVersion("community-7.2.0-14", &versionident.Version{
		Version:          "7.2.0",
		BuildNo:          14,
		CommunityEdition: true,
		Serverless:       false,
	})
	checkVersion("7.2.0-serverless", &versionident.Version{
		Version:          "7.2.0",
		BuildNo:          0,
		CommunityEdition: false,
		Serverless:       true,
	})
	checkVersion("community-7.2.0-14-serverless", &versionident.Version{
		Version:          "7.2.0",
		BuildNo:          14,
		CommunityEdition: true,
		Serverless:       true,
	})
	checkVersion("7", nil)
	checkVersion("invalid", nil)

}
