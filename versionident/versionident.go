package versionident

import (
	"context"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

type Version struct {
	Version          string
	BuildNo          int
	CommunityEdition bool
	Serverless       bool
}

func Identify(ctx context.Context, userInput string) (*Version, error) {
	editionPart := "enterprise"
	versionPart := ""
	buildNoPart := "0"

	versionParts := strings.Split(userInput, "-")

	lastVersionPartIdx := len(versionParts) - 1
	serverless := false
	if versionParts[lastVersionPartIdx] == "serverless" {
		versionParts = versionParts[:lastVersionPartIdx]
		serverless = true
	}

	if len(versionParts) == 1 {
		versionPart = versionParts[0]
	} else if len(versionParts) == 2 {
		if strings.Contains(versionParts[0], ".") {
			versionPart = versionParts[0]
			buildNoPart = versionParts[1]
		} else {
			editionPart = versionParts[0]
			versionPart = versionParts[1]
		}
	} else if len(versionParts) == 3 {
		editionPart = versionParts[0]
		versionPart = versionParts[1]
		buildNoPart = versionParts[2]
	}

	communityEdition := false
	if editionPart == "community" {
		communityEdition = true
	} else if editionPart == "enterprise" {
		communityEdition = false
	} else {
		return nil, errors.New("invalid version edition")
	}

	if len(strings.Split(versionPart, ".")) < 2 {
		return nil, errors.New("version number must be at least major.minor")
	}

	buildNo, err := strconv.ParseInt(buildNoPart, 10, 64)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse build number")
	}

	return &Version{
		Version:          versionPart,
		BuildNo:          int(buildNo),
		CommunityEdition: communityEdition,
		Serverless:       serverless,
	}, nil
}
