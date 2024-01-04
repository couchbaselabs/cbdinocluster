package caocontrol

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"
)

func generateX509Certificate(domainName string) ([]byte, []byte, error) {
	// Generate RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}

	domainName = "*." + domainName
	certTempl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cng", Organization: []string{"cng-org"}},
		DNSNames: 			   []string{"DNS:" + domainName},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0), // Valid for 1 year
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certData, err := x509.CreateCertificate(rand.Reader, &certTempl, &certTempl, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}

	cert, err := pemEncodeCert(certData)
	if err != nil {
		return nil, nil, err
	}

	key, err := pemEncodeKey(privateKey)
	if err != nil {
		return nil, nil, err
	}

	return cert, key, nil
}

func pemEncodeCert(certData []byte) ([]byte, error){
	block := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certData,
	}

	certPem := &bytes.Buffer{}
	if err := pem.Encode(certPem, block); err != nil {
		return nil, err
	}

	return certPem.Bytes(), nil
}

func pemEncodeKey(keyData *rsa.PrivateKey) ([]byte, error){
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(keyData),
	}

	key := &bytes.Buffer{}
	if err := pem.Encode(key, block); err != nil {
		return nil, err
	}

	return key.Bytes(), nil
}