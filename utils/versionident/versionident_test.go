package versionident_test

import (
	"context"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
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
				require.Equal(t, expected.ToyName, v.ToyName)
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

	checkVersion("8.6.0-23545+toy:brett", &versionident.Version{
		Version: "8.6.0",
		BuildNo: 23545,
		ToyName: "brett",
	})
	checkVersion("community-8.6.0-23545+toy:brett", &versionident.Version{
		Version:          "8.6.0",
		BuildNo:          23545,
		CommunityEdition: true,
		ToyName:          "brett",
	})
	checkVersion("8.6.0-23545+toy:jira-MB-12345", &versionident.Version{
		Version: "8.6.0",
		BuildNo: 23545,
		ToyName: "jira-MB-12345",
	})
	checkVersion("8.6.0-23545-serverless+toy:brett", &versionident.Version{
		Version:    "8.6.0",
		BuildNo:    23545,
		Serverless: true,
		ToyName:    "brett",
	})
	checkVersion("8.6.0+toy:brett", nil)
	checkVersion("8.6.0-23545+toy:", nil)
	checkVersion("8.6.0-23545+toy", nil)
	checkVersion("8.6.0-23545+toy:bad:name", nil)
	checkVersion("8.6.0-23545+brett", nil)
	checkVersion("8.6.0-23545+mb:12345", nil)

}
