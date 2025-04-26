package dinocerts

import "io"

// certRandReader is a special workaround for a trick they use in RSA to cause
// certificates to be non-deterministic even if you use a deterministic random
// source.
type certRandReader struct {
	Base io.Reader
}

func (a *certRandReader) Read(b []byte) (n int, err error) {
	if len(b) == 1 {
		b[0] = 0
		return 1, nil
	}

	return a.Base.Read(b)
}
