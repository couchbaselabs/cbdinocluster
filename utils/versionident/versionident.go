package versionident

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

type Version struct {
	Version          string
	BuildNo          int
	CommunityEdition bool
	Serverless       bool

	// ToyName is set for toy builds, specified as <version>+toy:<name>.
	ToyName string
}

var toyNameRegexp = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func Identify(ctx context.Context, userInput string) (*Version, error) {
	toyName := ""
	if versionPart, qualifier, hasQualifier := strings.Cut(userInput, "+"); hasQualifier {
		qualifierName, qualifierValue, _ := strings.Cut(qualifier, ":")
		if qualifierName != "toy" {
			return nil, errors.Errorf("unknown version qualifier `%s`", qualifierName)
		}

		if !toyNameRegexp.MatchString(qualifierValue) {
			return nil, errors.Errorf("invalid toy build name `%s`", qualifierValue)
		}

		userInput = versionPart
		toyName = qualifierValue
	}

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

	if toyName != "" && buildNo == 0 {
		return nil, errors.New("toy builds must specify a build number")
	}

	return &Version{
		Version:          versionPart,
		BuildNo:          int(buildNo),
		CommunityEdition: communityEdition,
		Serverless:       serverless,
		ToyName:          toyName,
	}, nil
}
