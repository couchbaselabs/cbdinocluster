package dockerdeploy

import (
	"context"
	"fmt"
	"net"

	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (d *Deployer) getClusterDinoCert(clusterID string) (*dinocerts.CertAuthority, []byte, error) {
	rootCa, err := dinocerts.GetRootCertAuthority()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get root dino ca")
	}

	fetchedClusterCa, err := rootCa.MakeIntermediaryCA("cluster-" + clusterID[:8])
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get cluster dino ca")
	}

	return fetchedClusterCa, rootCa.CertPem, nil
}

func (d *Deployer) setupNodeCertificates(
	ctx context.Context,
	node *ContainerInfo,
	clusterCa *dinocerts.CertAuthority,
	rootCaPem []byte,
) error {
	nodeIP := net.ParseIP(node.IPAddress)

	var dnsNames []string
	if node.DnsName != "" {
		dnsNames = append(dnsNames, node.DnsName)
	}
	if node.DnsSuffix != "" {
		dnsNames = append(dnsNames, node.DnsSuffix)
	}

	d.logger.Debug("generating node dinocert certificate",
		zap.String("node", node.NodeID),
		zap.Any("IP", nodeIP),
		zap.Any("dnsNames", dnsNames))

	certPem, keyPem, err := clusterCa.MakeServerCertificate("node-"+node.NodeID[:8], []net.IP{nodeIP}, dnsNames)
	if err != nil {
		return errors.Wrap(err, "failed to create server certificate")
	}

	d.logger.Debug("uploading dinocert certificates",
		zap.String("node", node.NodeID))

	var chainPem []byte
	chainPem = append(chainPem, certPem...)
	chainPem = append(chainPem, clusterCa.CertPem...)
	err = d.controller.UploadCertificates(ctx, node.ContainerID, chainPem, keyPem, [][]byte{rootCaPem})
	if err != nil {
		return errors.Wrap(err, "failed to upload certificates")
	}

	nodeCtrl := clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", node.IPAddress),
	}

	d.logger.Debug("refreshing trusted CAs for node",
		zap.String("node", node.NodeID))

	err = nodeCtrl.Controller().LoadTrustedCAs(ctx, &clustercontrol.LoadTrustedCAsOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to load trusted CAs")
	}

	d.logger.Debug("refreshing certificate for node",
		zap.String("node", node.NodeID))

	err = nodeCtrl.Controller().ReloadCertificate(ctx, &clustercontrol.ReloadCertificateOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to refresh certificates")
	}

	d.logger.Debug("removing default self-signed certificate for node",
		zap.String("node", node.NodeID))

	err = nodeCtrl.Controller().DeleteTrustedCA(ctx, &clustercontrol.DeleteTrustedCAOptions{
		ID: 0,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete default certificate")
	}

	return nil
}
