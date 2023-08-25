package cbdcuuid

import (
	"encoding/base32"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"github.com/pkg/errors"
)

type UUID [16]byte

func New() UUID {
	uuid := uuid.New()
	return [16]byte(uuid)
}

func Parse(str string) (UUID, error) {
	if len(str) == 32 {
		parsedUuid, err := hex.DecodeString(str)
		if err != nil {
			return UUID{}, errors.Wrap(err, "failed to parse hex uuid")
		}

		var uuid UUID
		copy(uuid[:], parsedUuid)
		return uuid, nil
	} else if len(str) == 26 {
		parsedUuid, err := base32.HexEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(str))
		if err != nil {
			return UUID{}, errors.Wrap(err, "failed to parse uuid")
		}

		var uuid UUID
		copy(uuid[:], parsedUuid)
		return uuid, nil
	}

	return UUID{}, errors.New("invalid uuid format")
}

func (uuid UUID) String() string {
	return hex.EncodeToString(uuid[:])
}

func (uuid UUID) ShortString() string {
	return strings.ToLower(
		base32.
			HexEncoding.
			WithPadding(base32.NoPadding).
			EncodeToString(uuid[:]))
}
