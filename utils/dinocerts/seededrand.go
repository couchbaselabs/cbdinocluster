package dinocerts

import (
	"hash/crc64"
	"math/rand"
)

var crcTbl *crc64.Table = crc64.MakeTable(crc64.ISO)

func checksum64(data []byte) uint64 {
	return crc64.Checksum(data, crcTbl)
}

func newSeededRand(seed string) *rand.Rand {
	rndSeed := checksum64([]byte(seed))
	rndSrc := rand.NewSource(int64(rndSeed))
	return rand.New(rndSrc)
}
