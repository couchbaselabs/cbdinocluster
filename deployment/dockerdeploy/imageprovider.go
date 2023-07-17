package dockerdeploy

import "context"

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
