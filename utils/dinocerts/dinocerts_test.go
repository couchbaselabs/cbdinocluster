package dinocerts_test

import (
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/stretchr/testify/require"
)

func TestSameRootAuthority(t *testing.T) {
	ca1, err := dinocerts.GetRootCertAuthority()
	require.NoError(t, err)

	ca2, err := dinocerts.GetRootCertAuthority()
	require.NoError(t, err)

	require.Equal(t, ca1, ca2)
}

func TestSameSeedEqualCerts(t *testing.T) {
	ca1, err := dinocerts.NewDinoCertAuthority("seed")
	require.NoError(t, err)

	ca2, err := dinocerts.NewDinoCertAuthority("seed")
	require.NoError(t, err)

	require.Equal(t, ca1.PrivKeyPem, ca2.PrivKeyPem)
	require.Equal(t, ca1.CertPem, ca2.CertPem)

	ica1, err := ca1.MakeIntermediaryCA("inter")
	require.NoError(t, err)

	ica2, err := ca2.MakeIntermediaryCA("inter")
	require.NoError(t, err)

	require.Equal(t, ica1.PrivKeyPem, ica2.PrivKeyPem)
	require.Equal(t, ica1.CertPem, ica2.CertPem)

	cert1, key1, err := ica1.MakeServerCertificate("server", nil, nil)
	require.NoError(t, err)

	cert2, key2, err := ica2.MakeServerCertificate("server", nil, nil)
	require.NoError(t, err)

	require.Equal(t, cert1, cert2)
	require.Equal(t, key1, key2)
}
