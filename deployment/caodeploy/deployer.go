package caodeploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/caocontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"go.uber.org/zap"
)

type Deployer struct {
	logger                                                           *zap.Logger
	client                                                           *caocontrol.Controller
	ghcrUser, ghcrToken                                              string
	operatorVer, operatorNamespace, admissionVer, admissionNamespace string
	crdPath                                                          string
	caoBinPath                                                       string
}

var _ deployment.Deployer = (*Deployer)(nil)

type NewDeployerOptions struct {
	Logger                                                           *zap.Logger
	Client                                                           *caocontrol.Controller
	GhcrUser, GhcrToken                                              string
	OperatorVer, OperatorNamespace, AdmissionVer, AdmissionNamespace string
	CrdPath                                                          string
	CaoBinPath                                                       string
}

func NewDeployer(opts *NewDeployerOptions) (*Deployer, error) {
	return &Deployer{
		logger:             opts.Logger,
		client:             opts.Client,
		ghcrUser:           opts.GhcrUser,
		ghcrToken:          opts.GhcrToken,
		crdPath:            opts.CrdPath,
		caoBinPath:         opts.CaoBinPath,
		operatorVer:        opts.OperatorVer,
		operatorNamespace:  opts.OperatorNamespace,
		admissionVer:       opts.AdmissionVer,
		admissionNamespace: opts.AdmissionNamespace,
	}, nil
}

// AllowNodeTraffic implements deployment.Deployer.
func (*Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("caodeploy does not support allowing node traffic")
}

// BlockNodeTraffic implements deployment.Deployer.
func (*Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("caodeploy does not support blocking node traffic")
}

// Cleanup implements deployment.Deployer.
func (*Deployer) Cleanup(ctx context.Context) error {
	return errors.New("caodeploy does not support cleanup")
}

// CollectLogs implements deployment.Deployer.
func (*Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	return []string{}, errors.New("caodeploy does not support collecting logs")
}

// CreateBucket implements deployment.Deployer.
func (*Deployer) CreateBucket(ctx context.Context, clusterID string, opts *deployment.CreateBucketOptions) error {
	return errors.New("caodeploy does not support creating buckets")
}

// CreateCollection implements deployment.Deployer.
func (*Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName string, scopeName string, collectionName string) error {
	return errors.New("caodeploy does not support creating collection")
}

// CreateScope implements deployment.Deployer.
func (*Deployer) CreateScope(ctx context.Context, clusterID string, bucketName string, scopeName string) error {
	return errors.New("caodeploy does not support creating scope")
}

// CreateUser implements deployment.Deployer.
func (*Deployer) CreateUser(ctx context.Context, clusterID string, opts *deployment.CreateUserOptions) error {
	return errors.New("caodeploy does not creating user")
}

// DeleteBucket implements deployment.Deployer.
func (*Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	return errors.New("caodeploy does not support deleting bucket")
}

// DeleteCollection implements deployment.Deployer.
func (*Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName string, scopeName string, collectionName string) error {
	return errors.New("caodeploy does not support deleting collection")
}

// DeleteScope implements deployment.Deployer.
func (*Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName string, scopeName string) error {
	return errors.New("caodeploy does not support deleting scope")
}

// DeleteUser implements deployment.Deployer.
func (*Deployer) DeleteUser(ctx context.Context, clusterID string, username string) error {
	return errors.New("caodeploy does not support deleting user")
}

// ExecuteQuery implements deployment.Deployer.
func (*Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error) {
	return "", errors.New("caodeploy does not support exectuing queries")
}

// GetCertificate implements deployment.Deployer.
func (*Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("caodeploy does not support getting certificates")
}

func (p *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	hostPort, err := p.client.GetRouteURL(p.operatorNamespace, clusterID)
	if err != nil {
		return nil, fmt.Errorf("could not retrive oc route host/port %s: %+v", p.operatorNamespace, err)
	}
	connStr := fmt.Sprintf("couchbase2://%s:%d", hostPort, 443)

	return &deployment.ConnectInfo{
		ConnStr: connStr,
		Mgmt:    "",
	}, nil
}

// GetDefinition implements deployment.Deployer.
func (*Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	return nil, errors.New("caodeploy does not support get definition")
}

// ListBuckets implements deployment.Deployer.
func (*Deployer) ListBuckets(ctx context.Context, clusterID string) ([]deployment.BucketInfo, error) {
	return nil, errors.New("caodeploy does not support listing buckets")
}

func (p *Deployer) listClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	clusterIDs, err := p.client.ListCouchbaseClusters(p.operatorNamespace)
	if err != nil {
		return nil, fmt.Errorf("no couchbase cluster exists in namspace: %s: %+v", p.operatorNamespace, err)
	}
	var out []deployment.ClusterInfo

	for _, clusterID := range clusterIDs {
		out = append(out, ClusterInfo{ClusterID: clusterID, State: "Avaiable"})
	}
	return out, nil
}

func (p *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	return p.listClusters(ctx)
}

// ListCollections implements deployment.Deployer.
func (*Deployer) ListCollections(ctx context.Context, clusterID string, bucketName string) ([]deployment.ScopeInfo, error) {
	return nil, errors.New("caodeploy does not support listing collections")
}

// ListImages implements deployment.Deployer.
func (*Deployer) ListImages(ctx context.Context) ([]deployment.Image, error) {
	return nil, errors.New("caodeploy does not support listing images")
}

// ListUsers implements deployment.Deployer.
func (*Deployer) ListUsers(ctx context.Context, clusterID string) ([]deployment.UserInfo, error) {
	return nil, errors.New("caodeploy does not support listing users")
}

// ModifyCluster implements deployment.Deployer.
func (*Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	return errors.New("caodeploy does not support modifying clusters")
}

func (p *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	p.logger.Debug("creating new cb cluster")

	p.logger.Debug("retrieving k8s ditribution info")

	if !caocontrol.IsClusterOpenshift(p.client.K8sClient) {
		// As for other K8s distros/services, we need Ingress controller to be presenet
		// which might not come with many k8s cluster and need seperate installation
		// This needs additional checks on cbdinocluster side before we carry out the installation process.
		// TODO: add an issue for future work.
		return nil, fmt.Errorf("needs Openshift cluster to deploy via cao; other k8s clusters not supported yet..")
	}

	// Atm, cao doesn't allow different server versions for diff cb nodes inside same cluster
	// All nodes must have SAME version. Hence, treating NodeGroup as a cbc resource (1:1) for now.
	// There is K8S ticket (K8S-3320) which will allow granular version management/upgrades at node level.
	// Once done, this could be revisited.
	if len(def.NodeGroups) != 1 {
		return nil, fmt.Errorf("only one node group in cluster def can exist")
	}

	nodeGroup := def.NodeGroups[0]

	p.logger.Debug("fetching cluster host domain info")

	if p.client.NeedGhcrAccess(p.ghcrUser, p.ghcrToken) {
		p.logger.Debug("creating ghcr secrets")
		err := p.client.CreateGhcrSecret(p.ghcrUser, p.ghcrToken, p.operatorNamespace)
		if err != nil {
			return nil, fmt.Errorf("error creating ghcr secret: %v", err)
		}
	}

	p.logger.Debug("installing crds")

	err := p.client.InstallCRD(p.crdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create CRD: %v", err)
	}

	p.logger.Debug("installing admission controller")

	err = p.client.CreateAdmission(p.admissionVer, p.admissionNamespace, p.caoBinPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create admission: %v", err)
	}

	p.logger.Debug("installing operator")

	err = p.client.CreateOperator(p.operatorVer, p.operatorNamespace, p.caoBinPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create operator: %v", err)
	}

	p.logger.Debug("creating admin secret for cbc resource")

	err = p.client.CreateCbcAdminSecret(p.operatorNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create cbc admin secret: %v", err)
	}

	p.logger.Debug("creating cbc resource")

	// same as cbc k8s resource name
	clusterID := cbdcuuid.New()

	cbcc := p.client.NewCbcConfig(clusterID.ShortString(), nodeGroup, false)
	cbcc.WithAdminSecret(caocontrol.CbdcCbcAdminSecretName)
	cbcc.WithCNGTLS(clusterID.ShortString() + caocontrol.CNGTLSSecretNamePrefix)

	if cbcc.IsCNGTLSEnabled() {
		p.logger.Debug("creating cng tls secret resource")

		err = p.client.CreateCNGTLSSecret(p.client.HostDomain, cbcc.Spec.Networking.CloudNativeGateway.TLS.ServerSecretName, p.operatorNamespace)
		if err != nil {
			return nil, fmt.Errorf("failed to create cng tls secret resource: %v", err)
		}
	}

	err = p.client.CreateCouchbaseCluster(cbcc, p.operatorNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create couchbase cluster: %v", err)
	}

	p.logger.Debug("creating external access to cng from outside k8s cluster")

	err = p.client.CreateExternalAccess(clusterID.ShortString()+caocontrol.CNGServiceNamePrefix, p.operatorNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create external access: %v", err)
	}

	return ClusterInfo{ClusterID: clusterID.ShortString()}, nil

}

// RemoveAll implements deployment.Deployer.
func (*Deployer) RemoveAll(ctx context.Context) error {
	return errors.New("caodeploy does not support removing all clusters")
}

// RemoveCluster implements deployment.Deployer.
func (*Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	return errors.New("caodeploy does not support removing a cluster")
}

// SearchImages implements deployment.Deployer.
func (*Deployer) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	return nil, errors.New("caodeploy does not support searching images")
}

// UpdateClusterExpiry implements deployment.Deployer.
func (*Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	return errors.New("caodeploy does not support updating cluster expiry")
}
