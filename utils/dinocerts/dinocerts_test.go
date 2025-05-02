package dinocerts_test

import (
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/stretchr/testify/require"
)

// These tests ensure that dinocerts produces the same certificates and keys
// when given the same seed.  This is important for testing purposes, as we want
// to be able to reproduce the same certificates and keys across different runs
// of the tests.  Note that we primarily cache the keys that we generate so we
// test with direct generation of the keys to ensure they are the same, and then
// we test all certs are the same (which are not cached).

// Because the way that rsa.GenerateKeys works is that it has to search for prime
// numbers.  We've used fastseedfinder to identify the fastest seeds to use for
// these tests to avoid the tests taking unneccessarily long.

func TestSameKey(t *testing.T) {
	key1, err := dinocerts.GenerateKeyUncached("j")
	require.NoError(t, err)

	key2, err := dinocerts.GenerateKeyUncached("j")
	require.NoError(t, err)

	require.Equal(t, key1, key2)
}

func TestSameRootAuthority(t *testing.T) {
	ca1, err := dinocerts.GetRootCertAuthority()
	require.NoError(t, err)

	ca2, err := dinocerts.GetRootCertAuthority()
	require.NoError(t, err)

	require.Equal(t, ca1, ca2)
}

func TestSameSeedEqualCerts(t *testing.T) {
	ca1, err := dinocerts.NewDinoCertAuthority("j")
	require.NoError(t, err)

	ca2, err := dinocerts.NewDinoCertAuthority("j")
	require.NoError(t, err)

	require.Equal(t, ca1.PrivKeyPem, ca2.PrivKeyPem)
	require.Equal(t, ca1.CertPem, ca2.CertPem)

	ica1, err := ca1.MakeIntermediaryCA("d")
	require.NoError(t, err)

	ica2, err := ca2.MakeIntermediaryCA("d")
	require.NoError(t, err)

	require.Equal(t, ica1.PrivKeyPem, ica2.PrivKeyPem)
	require.Equal(t, ica1.CertPem, ica2.CertPem)

	cert1, key1, err := ica1.MakeServerCertificate("P", nil, nil)
	require.NoError(t, err)

	cert2, key2, err := ica2.MakeServerCertificate("P", nil, nil)
	require.NoError(t, err)

	require.Equal(t, cert1, cert2)
	require.Equal(t, key1, key2)
}
