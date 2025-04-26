package dinocerts

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"

	"github.com/denisbrodbeck/machineid"
)

// CertAuthority provides a mechanism to produce deterministic ca's and certificates
// that can be used for testing purposes.  The certificates are generated using a seeded
// random number generator, so the same seed will produce the same certificate every time.
// Note that these deterministic certificates may change over time if the Go standard library
// changes the way it generates certificates, so use with caution.
type CertAuthority struct {
	Cert       *x509.Certificate
	PrivKey    *rsa.PrivateKey
	CertBytes  []byte
	CertPem    []byte
	PrivKeyPem []byte
}

func makeDinoCertAuthority(seed string, parent *CertAuthority) (*CertAuthority, error) {
	rnd := newSeededRand(seed)
	certRnd := &certRandReader{rnd}

	privKey, err := rsa.GenerateKey(certRnd, 4096)
	if err != nil {
		return nil, err
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "dinocert-" + seed,
		},
		NotBefore:             time.Date(2025, 01, 01, 00, 00, 00, 00, time.UTC),
		NotAfter:              time.Date(2035, 01, 01, 00, 00, 00, 00, time.UTC),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	parentCert := cert
	parentKey := privKey
	if parent != nil {
		parentCert = parent.Cert
		parentKey = parent.PrivKey
	}

	certBytes, err := x509.CreateCertificate(nil, cert, parentCert, &privKey.PublicKey, parentKey)
	if err != nil {
		return nil, err
	}

	certPem := new(bytes.Buffer)
	pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	privKeyPem := new(bytes.Buffer)
	pem.Encode(privKeyPem, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	return &CertAuthority{
		Cert:       cert,
		PrivKey:    privKey,
		CertBytes:  certBytes,
		CertPem:    certPem.Bytes(),
		PrivKeyPem: privKeyPem.Bytes(),
	}, nil
}

func NewDinoCertAuthority(seed string) (*CertAuthority, error) {
	return makeDinoCertAuthority(seed, nil)
}

var rootCertAuthority *CertAuthority = nil

func GetRootCertAuthority() (*CertAuthority, error) {
	// we cache the cert authority here to avoid needing to regenerate the
	// key (which takes a while) as well as look up the users machine ID.
	if rootCertAuthority != nil {
		return rootCertAuthority, nil
	}

	machineId, err := machineid.ID()
	if err != nil {
		return nil, err
	}

	certAuthority, err := NewDinoCertAuthority(machineId)
	if err != nil {
		return nil, err
	}

	rootCertAuthority = certAuthority
	return rootCertAuthority, nil
}

func (d *CertAuthority) MakeIntermediaryCA(
	seed string,
) (*CertAuthority, error) {
	return makeDinoCertAuthority(seed, d)
}

func (d *CertAuthority) MakeServerCertificate(
	seed string,
	ipAddresses []net.IP,
	dnsNames []string,
) ([]byte, []byte, error) {
	rnd := newSeededRand(seed)

	privKey, err := rsa.GenerateKey(&certRandReader{rnd}, 4096)
	if err != nil {
		return nil, nil, err
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "dinocert-" + seed,
		},
		IPAddresses:  ipAddresses,
		DNSNames:     dnsNames,
		NotBefore:    time.Date(2025, 01, 01, 00, 00, 00, 00, time.UTC),
		NotAfter:     time.Date(2035, 01, 01, 00, 00, 00, 00, time.UTC),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	certBytes, err := x509.CreateCertificate(nil, cert, d.Cert, &privKey.PublicKey, d.PrivKey)
	if err != nil {
		return nil, nil, err
	}

	certPem := new(bytes.Buffer)
	pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	privKeyPem := new(bytes.Buffer)
	pem.Encode(privKeyPem, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	return certPem.Bytes(), privKeyPem.Bytes(), nil
}
