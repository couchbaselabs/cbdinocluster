package caodeploy

import (
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/stretchr/testify/require"
)

func TestMakeCngCertificateVerifiesForRouteHost(t *testing.T) {
	rootCa, err := dinocerts.NewDinoCertAuthority("test-root")
	require.NoError(t, err)

	clusterID := "0123456789abcdef0123456789abcdef"
	namespace := "cbdc2-" + clusterID
	cngHost := "cng-" + namespace + ".apps.example.com"

	chainPem, keyPem, caPem, err := makeCngCertificate(rootCa, namespace, clusterID, cngHost)
	require.NoError(t, err)

	pair, err := tls.X509KeyPair(chainPem, keyPem)
	require.NoError(t, err)
	require.Len(t, pair.Certificate, 2)

	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)
	intermediate, err := x509.ParseCertificate(pair.Certificate[1])
	require.NoError(t, err)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caPem))
	intermediates := x509.NewCertPool()
	intermediates.AddCert(intermediate)

	for _, host := range []string{cngHost, CngServiceName + "." + namespace + ".svc"} {
		_, err = leaf.Verify(x509.VerifyOptions{
			DNSName:       host,
			Roots:         roots,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		require.NoError(t, err, host)
	}

	_, err = leaf.Verify(x509.VerifyOptions{
		DNSName:       "ui-" + namespace + ".apps.example.com",
		Roots:         roots,
		Intermediates: intermediates,
	})
	require.Error(t, err)
}
