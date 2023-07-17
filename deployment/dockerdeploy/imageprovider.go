package dockerdeploy

import (
	"context"

	"golang.org/x/mod/semver"
)

type ImageDef struct {
	Version             string
	BuildNo             int
	UseCommunityEdition bool
	UseServerless       bool
}

type ImageRef struct {
	ImagePath string
}

type ImageProvider interface {
	GetImage(ctx context.Context, def *ImageDef) (*ImageRef, error)
}

func CompareImageDefs(a, b *ImageDef) int {
	c := semver.Compare(a.Version, b.Version)
	if c != 0 {
		return c
	}

	if a.BuildNo < b.BuildNo {
		return -1
	} else if a.BuildNo > b.BuildNo {
		return +1
	}

	if a.UseCommunityEdition && !b.UseCommunityEdition {
		return -1
	} else if !a.UseCommunityEdition && b.UseCommunityEdition {
		return +1
	}

	if !a.UseServerless && b.UseServerless {
		return -1
	} else if a.UseServerless && !b.UseServerless {
		return +1
	}

	return 0
}
