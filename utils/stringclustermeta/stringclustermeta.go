package stringclustermeta

import (
	"fmt"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/pkg/errors"
)

type MetaData struct {
	ID      cbdcuuid.UUID
	Expiry  time.Time
	Purpose string
}

func (m *MetaData) String() string {
	shortID := m.ID.ShortString()
	expiry := m.Expiry.UTC().Format("20060102-150405")

	if m.Purpose != "" {
		return fmt.Sprintf("cbdc2_%s_%s_%s", shortID, expiry, m.Purpose)
	} else {
		return fmt.Sprintf("cbdc2_%s_%s", shortID, expiry)
	}
}

func Parse(str string) (*MetaData, error) {
	strParts := strings.SplitN(str, "_", 4)

	if len(strParts) < 3 {
		return nil, nil
	}
	if strParts[0] != "cbdc2" {
		return nil, nil
	}

	parsedID, err := cbdcuuid.Parse(strParts[1])
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse id")
	}

	expiryStr := strParts[2]
	expiryTime, err := time.Parse("20060102-150405", expiryStr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse expiry time")
	}

	var purpose string
	if len(strParts) >= 4 {
		purpose = strParts[3]
	}

	return &MetaData{
		ID:      parsedID,
		Expiry:  expiryTime,
		Purpose: purpose,
	}, nil
}
