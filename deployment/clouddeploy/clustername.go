package clouddeploy

import (
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
)

// maxClusterNameLen is the limit the v4 api puts on a cluster name.
const maxClusterNameLen = 256

// metaNameFor encodes the cluster identity into a name of at most maxLen
// bytes. It trims the end of the purpose to fit and keeps the name valid
// UTF-8. It also returns the purpose the name holds. The api counts
// characters, so a byte limit never gives a name that is too long.
func metaNameFor(
	meta stringclustermeta.MetaData,
	maxLen int,
) (name string, purpose string, trimmed bool) {
	name = meta.String()
	if len(name) <= maxLen {
		return name, meta.Purpose, false
	}

	keep := len(meta.Purpose) - (len(name) - maxLen)
	if keep < 0 {
		keep = 0
	}
	for keep > 0 && !utf8.RuneStart(meta.Purpose[keep]) {
		keep--
	}
	meta.Purpose = meta.Purpose[:keep]

	return meta.String(), meta.Purpose, true
}

// clusterNameFor encodes the cluster identity into the cluster name. A long
// purpose is trimmed so the create cannot fail on length. It also returns the
// purpose the name holds.
func (p *Deployer) clusterNameFor(meta stringclustermeta.MetaData) (name string, purpose string) {
	name, purpose, trimmed := metaNameFor(meta, maxClusterNameLen)
	if trimmed {
		p.logger.Warn("trimmed the purpose to fit the cluster name limit",
			zap.String("purpose", purpose))
	}
	return name, purpose
}
