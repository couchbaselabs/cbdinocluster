package caodeploy

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/stretchr/testify/require"
)

const testClusterID = "abcdef0123456789"

func parseCerts(t *testing.T, pemBytes []byte) []*x509.Certificate {
	t.Helper()

	var certs []*x509.Certificate
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		require.Equal(t, "CERTIFICATE", block.Type)

		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		certs = append(certs, cert)
	}

	require.NotEmpty(t, certs, "expected at least one PEM certificate")
	return certs
}

// The gateway CA must be a real CA signed by the Root Dino CA.
func TestGatewayCAIsSignedByRoot(t *testing.T) {
	root, err := dinocerts.GetRootCertAuthority()
	require.NoError(t, err)

	gatewayCa, rootCaPem, err := getGatewayDinoCA(testClusterID)
	require.NoError(t, err)

	require.Equal(t, string(root.CertPem), string(rootCaPem))

	gatewayCaCert := parseCerts(t, gatewayCa.CertPem)[0]
	require.True(t, gatewayCaCert.IsCA, "gateway CA must be a CA")
	require.NotZero(t, gatewayCaCert.KeyUsage&x509.KeyUsageCertSign, "gateway CA must be allowed to sign certs")

	rootCert := parseCerts(t, root.CertPem)[0]
	roots := x509.NewCertPool()
	roots.AddCert(rootCert)
	_, err = gatewayCaCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	require.NoError(t, err, "gateway CA must verify against the Root Dino CA")
}

// The provisioned secret must form a full chain: leaf -> gateway CA -> Root Dino CA.
func TestGatewayTLSSecretChainValidates(t *testing.T) {
	root, err := dinocerts.GetRootCertAuthority()
	require.NoError(t, err)
	rootCert := parseCerts(t, root.CertPem)[0]

	dnsNames := []string{"cluster-cloud-native-gateway-service", "cng-test.example.com"}
	secretData, err := buildGatewayTLSSecretData(testClusterID, dnsNames, nil)
	require.NoError(t, err)

	// tls.crt is a chain of [leaf, gateway CA].
	tlsCerts := parseCerts(t, secretData["tls.crt"])
	require.Len(t, tlsCerts, 2)
	leaf := tlsCerts[0]
	require.False(t, leaf.IsCA, "tls.crt must lead with a leaf certificate")
	require.ElementsMatch(t, dnsNames, leaf.DNSNames)

	// ca.crt is a bundle of [gateway CA, root].
	caCerts := parseCerts(t, secretData["ca.crt"])
	require.Len(t, caCerts, 2)
	gatewayCaCert := caCerts[0]
	require.True(t, gatewayCaCert.IsCA, "ca.crt must lead with the gateway CA")
	require.Equal(t, rootCert.Subject.String(), caCerts[1].Subject.String())

	// Leaf must have its own subject and key and be signed by the CA, not be a
	// copy of the CA that looks self-signed.
	require.NotEqual(t, gatewayCaCert.Subject.String(), leaf.Subject.String(),
		"leaf and gateway CA must not share a subject")
	require.NotEqual(t, leaf.SubjectKeyId, leaf.AuthorityKeyId,
		"leaf must be signed by the CA's key, not its own")
	require.Equal(t, gatewayCaCert.SubjectKeyId, leaf.AuthorityKeyId,
		"leaf's authority key id must point at the gateway CA")

	// The leaf must validate up to the root using the gateway CA as intermediate.
	roots := x509.NewCertPool()
	roots.AddCert(rootCert)
	intermediates := x509.NewCertPool()
	intermediates.AddCert(caCerts[0])

	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       "cng-test.example.com",
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	require.NoError(t, err, "gateway leaf must chain to the Root Dino CA")
}

// `get-gateway-ca` must return the CA that signs the leaf, not the leaf itself.
func TestGetGatewayCertificateMatchesProvisionedCA(t *testing.T) {
	gatewayCa, _, err := getGatewayDinoCA(testClusterID)
	require.NoError(t, err)

	secretData, err := buildGatewayTLSSecretData(testClusterID, nil, nil)
	require.NoError(t, err)

	returnedCA := parseCerts(t, gatewayCa.CertPem)[0]
	require.True(t, returnedCA.IsCA)

	leaf := parseCerts(t, secretData["tls.crt"])[0]
	require.False(t, leaf.IsCA)

	require.NoError(t, leaf.CheckSignatureFrom(returnedCA),
		"provisioned leaf must be signed by the gateway CA returned by get-gateway-ca")
}
