package caodeploy

import (
	"context"
	"fmt"
	"net"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CngTlsSecretName holds our Dino-signed gateway cert. Without it the operator
// serves its own self-signed cert, which doesn't chain to the Dino root.
const CngTlsSecretName = "cbdc2-cng-tls"

// getGatewayDinoCA returns the cluster's gateway CA: an intermediate signed by
// the Root Dino CA, derived deterministically from the cluster id.
func getGatewayDinoCA(clusterID string) (*dinocerts.CertAuthority, []byte, error) {
	rootCa, err := dinocerts.GetRootCertAuthority()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get root dino ca")
	}

	gatewayCa, err := rootCa.MakeIntermediaryCA("gateway-" + clusterID[:8])
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to make gateway dino ca")
	}

	return gatewayCa, rootCa.CertPem, nil
}

// buildGatewayTLSSecretData builds the gateway TLS secret: tls.crt is leaf +
// gateway CA, tls.key the leaf key, ca.crt gateway CA + Root Dino CA.
func buildGatewayTLSSecretData(
	clusterID string,
	dnsNames []string,
	ipAddresses []net.IP,
) (map[string][]byte, error) {
	gatewayCa, rootCaPem, err := getGatewayDinoCA(clusterID)
	if err != nil {
		return nil, err
	}

	// Name the leaf distinctly from the CA, or it reuses the CA's key and
	// subject and looks self-signed.
	leafPem, keyPem, err := gatewayCa.MakeServerCertificate("gateway-server-"+clusterID[:8], ipAddresses, dnsNames)
	if err != nil {
		return nil, errors.Wrap(err, "failed to make gateway server certificate")
	}

	var tlsChain []byte
	tlsChain = append(tlsChain, leafPem...)
	tlsChain = append(tlsChain, gatewayCa.CertPem...)

	var caChain []byte
	caChain = append(caChain, gatewayCa.CertPem...)
	caChain = append(caChain, rootCaPem...)

	return map[string][]byte{
		"tls.crt": tlsChain,
		"tls.key": keyPem,
		"ca.crt":  caChain,
	}, nil
}

// gatewayServerDNSNames lists the hostnames the gateway leaf is valid for.
// NodePort access is by node IP (not known here), so that path relies on the
// SDK skipping the hostname check.
func (d *Deployer) gatewayServerDNSNames(ctx context.Context, clusterID string, namespace string) []string {
	dnsNames := []string{
		"localhost",
		CngServiceName,
		fmt.Sprintf("%s.%s", CngServiceName, namespace),
		fmt.Sprintf("%s.%s.svc", CngServiceName, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", CngServiceName, namespace),
	}

	if d.sharedGateway != "" {
		baseDomain, err := d.getSharedGatewayBaseDomain(ctx)
		if err != nil {
			d.logger.Warn("failed to resolve shared gateway base domain for gateway cert", zap.Error(err))
		} else {
			dnsNames = append(dnsNames, fmt.Sprintf("cng-%s.%s", clusterID, baseDomain))
		}
	}

	return dnsNames
}

func (d *Deployer) provisionGatewayTLSSecret(ctx context.Context, clusterID string, namespace string) error {
	dnsNames := d.gatewayServerDNSNames(ctx, clusterID, namespace)

	secretData, err := buildGatewayTLSSecretData(clusterID, dnsNames, nil)
	if err != nil {
		return errors.Wrap(err, "failed to build gateway tls secret data")
	}

	d.logger.Info("provisioning dino gateway tls secret",
		zap.String("secret", CngTlsSecretName),
		zap.Strings("dnsNames", dnsNames))

	err = d.client.CreateSecret(ctx, namespace, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: CngTlsSecretName,
		},
		Data: secretData,
		Type: corev1.SecretTypeTLS,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create gateway tls secret")
	}

	return nil
}
