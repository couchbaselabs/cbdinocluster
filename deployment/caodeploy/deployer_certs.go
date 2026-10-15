package caodeploy

import (
	"context"
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// CngTlsSecretName holds the dinocert-issued Cloud Native Gateway server certificate.
	// Clusters without it use the operator's self-signed certificate, which is only
	// valid for an in-cluster service name and so can't be verified through a route.
	CngTlsSecretName = "cbdc2-cng-tls"

	cngRouteName      = "cng"
	cngHostAnnotation = "cbdc2.cng_host"
	cngTlsCaKey       = "ca.crt"
)

// cngRouteHost computes the host the cng route will be exposed on.  This matches the
// host OpenShift would generate, but we set it on the route explicitly so that it's
// known before the cluster exists and the gateway certificate can be issued for it.
func (d *Deployer) cngRouteHost(ctx context.Context, namespace string) (string, error) {
	domain, err := d.client.GetIngressDomain(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to get ingress domain")
	}

	return fmt.Sprintf("%s-%s.%s", cngRouteName, namespace, domain), nil
}

// makeCngCertificate issues a gateway server certificate for cngHost (and the in-cluster
// service name) via a per-cluster intermediary of rootCa.  Returns the certificate chain,
// its private key, and the root CA certificate clients should trust.
func makeCngCertificate(
	rootCa *dinocerts.CertAuthority,
	namespace string,
	clusterID string,
	cngHost string,
) ([]byte, []byte, []byte, error) {
	clusterCa, err := rootCa.MakeIntermediaryCA("cluster-" + clusterID[:8])
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to get cluster dino ca")
	}

	dnsNames := []string{
		cngHost,
		fmt.Sprintf("%s.%s.svc", CngServiceName, namespace),
	}

	certPem, keyPem, err := clusterCa.MakeServerCertificate("cng-"+clusterID[:8], nil, dnsNames)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "failed to create cng server certificate")
	}

	var chainPem []byte
	chainPem = append(chainPem, certPem...)
	chainPem = append(chainPem, clusterCa.CertPem...)

	return chainPem, keyPem, rootCa.CertPem, nil
}

// createCngTlsSecret issues the Cloud Native Gateway a server certificate for its
// route host, signed by the dinocert root CA, which is stored alongside it as ca.crt.
func (d *Deployer) createCngTlsSecret(ctx context.Context, namespace string, clusterID string, cngHost string) error {
	rootCa, err := dinocerts.GetRootCertAuthority()
	if err != nil {
		return errors.Wrap(err, "failed to get root dino ca")
	}

	d.logger.Debug("generating cng dinocert certificate", zap.String("host", cngHost))

	chainPem, keyPem, caPem, err := makeCngCertificate(rootCa, namespace, clusterID, cngHost)
	if err != nil {
		return err
	}

	err = d.client.CreateSecret(ctx, namespace, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: CngTlsSecretName,
			Annotations: map[string]string{
				cngHostAnnotation: cngHost,
			},
		},
		Data: map[string][]byte{
			corev1.TLSCertKey:       chainPem,
			corev1.TLSPrivateKeyKey: keyPem,
			cngTlsCaKey:             caPem,
		},
		Type: corev1.SecretTypeTLS,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create cng tls secret")
	}

	return nil
}

// getCngTlsSecret returns the cluster's dinocert gateway secret, or nil if the
// cluster's gateway uses the operator's self-signed certificate instead.
func (d *Deployer) getCngTlsSecret(ctx context.Context, namespace string) (*corev1.Secret, error) {
	secret, err := d.client.GetSecret(ctx, namespace, CngTlsSecretName)
	if err != nil {
		if apierrors.IsNotFound(errors.Cause(err)) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "failed to get cng tls secret")
	}

	return secret, nil
}
