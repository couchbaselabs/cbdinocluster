package cloudprovision

import (
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type ProjectNameMetaData struct {
	ID     string
	Expiry time.Time
}

func ParseProjectNameMetaData(projectName string) (*ProjectNameMetaData, error) {
	projectNameParts := strings.Split(projectName, "_")

	if len(projectNameParts) != 3 {
		return nil, nil
	}
	if projectNameParts[0] != "cbdc2" {
		return nil, nil
	}

	parsedID := projectNameParts[1]
	expiryUnixSecsStr := projectNameParts[2]

	expiryUnixSecs, err := strconv.ParseUint(expiryUnixSecsStr, 10, 64)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse an expected timestamp")
	}

	expiryTime := time.Unix(int64(expiryUnixSecs), 0)

	return &ProjectNameMetaData{
		ID:     parsedID,
		Expiry: expiryTime,
	}, nil
}
