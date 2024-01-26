package caodeploy

import (
	"context"
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/caocontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const (
	CouchbaseClusterName = "cluster"
)

type Deployer struct {
	logger *zap.Logger
	client *caocontrol.Controller
}

var _ deployment.Deployer = (*Deployer)(nil)

type NewDeployerOptions struct {
	Logger *zap.Logger
	Client *caocontrol.Controller
}

func NewDeployer(opts *NewDeployerOptions) (*Deployer, error) {
	return &Deployer{
		logger: opts.Logger,
		client: opts.Client,
	}, nil
}

func (d *Deployer) formatExpiry(expiry time.Time) string {
	if expiry.IsZero() {
		return "none"
	}
	return expiry.UTC().Format("20060102-150405")
}

func (d *Deployer) parseExpiry(expiryStr string) (time.Time, error) {
	if expiryStr == "none" || expiryStr == "" {
		return time.Time{}, nil
	}

	expiryTime, err := time.Parse("20060102-150405", expiryStr)
	if err != nil {
		return time.Time{}, errors.Wrap(err, "failed to parse expiry time")
	}

	return expiryTime, nil
}

func (d *Deployer) GetClient() *caocontrol.Controller {
	return d.client
}

func (d *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	namespaces, err := d.client.ListNamespaces(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list namespaces")
	}

	var clusters []deployment.ClusterInfo
	for _, namespace := range namespaces.Items {
		if namespace.Labels["cbdc2.cluster_id"] != "" {
			clusterStatus := "broken"

			cluster, err := d.client.GetCouchbaseCluster(ctx, namespace.Name, CouchbaseClusterName)
			if err != nil {
				d.logger.Debug("failed to read cluster info", zap.Error(err))
			} else {
				status, err := d.client.ParseCouchbaseClusterStatus(cluster)
				if err != nil {
					d.logger.Debug("failed to parse cluster status", zap.Error(err))
				} else {
					for _, condition := range status.Conditions {
						if condition.Type == "Available" {
							if condition.Status == "True" {
								clusterStatus = "available"
							} else if condition.Reason == "Creating" {
								clusterStatus = "creating"
							}
						}
					}
				}
			}

			var expiryTime time.Time
			expiryStr := namespace.Labels["cbdc2.expiry"]
			if expiryStr != "" {
				expiryTime, err = d.parseExpiry(expiryStr)
				if err != nil {
					d.logger.Debug("failed to parse cluster expiry", zap.Error(err))
				}
			}

			clusters = append(clusters, &ClusterInfo{
				ClusterID: namespace.Labels["cbdc2.cluster_id"],
				Expiry:    expiryTime,
				State:     clusterStatus,
			})
		}
	}

	return clusters, nil
}

func (d *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	clusterID := cbdcuuid.New()

	namespace := "cbdc2-" + clusterID.String()

	expiryTime := time.Time{}
	if def.Expiry > 0 {
		expiryTime = time.Now().Add(def.Expiry)
	}

	err := d.client.CreateNamespace(ctx, namespace, map[string]string{
		"cbdc2.type":       "cluster",
		"cbdc2.cluster_id": clusterID.String(),
		"cbdc2.purpose":    def.Purpose,
		"cbdc2.expiry":     d.formatExpiry(expiryTime),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cluster namespace")
	}

	err = d.client.InstallGhcrSecret(ctx, namespace)
	if err != nil {
		return nil, errors.Wrap(err, "failed to install ghcr secret")
	}

	err = d.client.InstallOperator(ctx, namespace)
	if err != nil {
		return nil, errors.Wrap(err, "failed to install operator")
	}

	err = d.client.CreateBasicAuthSecret(ctx, namespace, "cbdc2-admin-auth", "Administrator", "password")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create admin auth")
	}

	clusterVersion := ""

	var serversRes []interface{}
	for nodeGrpIdx, nodeGrp := range def.NodeGroups {
		caoServices, err := clusterdef.ServicesToCaoServices(nodeGrp.Services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate cao server services list")
		}

		if clusterVersion == "" {
			clusterVersion = nodeGrp.Version
		}
		if clusterVersion != nodeGrp.Version {
			return nil, errors.New("all node groups must have the same couchbase version")
		}

		serversRes = append(serversRes, map[string]interface{}{
			"size":     nodeGrp.Count,
			"name":     fmt.Sprintf("group_%d", nodeGrpIdx),
			"services": caoServices,
			"pod": map[string]interface{}{
				"spec": map[string]interface{}{
					"imagePullSecrets": []map[string]interface{}{
						{
							"name": caocontrol.GhcrSecretName,
						},
					},
				},
			},
		})
	}

	clusterSpec := map[string]interface{}{
		"image": "couchbase/server:" + clusterVersion,
		"buckets": map[string]interface{}{
			"managed": false,
		},
		"security": map[string]interface{}{
			"adminSecret": "cbdc2-admin-auth",
			"rbac": map[string]interface{}{
				"managed": true,
			},
		},
		"networking": map[string]interface{}{
			"exposeAdminConsole": true,
			"exposedFeatures":    []string{"admin", "xdcr", "client"},
		},
		"servers": serversRes,
	}

	err = d.client.CreateCouchbaseCluster(ctx, namespace, CouchbaseClusterName, nil, clusterSpec)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cluster resource")
	}

	return ClusterInfo{
		ClusterID: clusterID.String(),
		Expiry:    time.Time{},
		State:     "running",
	}, nil
}

func (d *Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	return nil, errors.New("caodeploy does not support fetching the cluster definition")
}

func (d *Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	return errors.New("caodeploy does not support updating cluster expiry")
}

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	return errors.New("caodeploy does not support modifying clusters")
}

func (d *Deployer) getClusterNamespace(ctx context.Context, clusterID string) (string, error) {
	namespaces, err := d.client.ListNamespaces(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to list namespaces")
	}

	var namespaceName string
	for _, namespace := range namespaces.Items {
		if namespace.Labels["cbdc2.cluster_id"] == clusterID {
			namespaceName = namespace.Name
		}
	}

	return namespaceName, nil
}

func (d *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	namespaceName, err := d.getClusterNamespace(ctx, clusterID)
	if err != nil {
		return err
	}

	if namespaceName != "" {
		err = d.client.DeleteNamespaces(ctx, []string{namespaceName})
		if err != nil {
			return errors.Wrap(err, "failed delete namespaces")
		}
	}

	return nil
}

func (d *Deployer) RemoveAll(ctx context.Context) error {
	namespaces, err := d.client.ListNamespaces(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list namespaces")
	}

	var clusterNames []string
	for _, namespace := range namespaces.Items {
		if namespace.Labels["cbdc2.cluster_id"] != "" {
			clusterNames = append(clusterNames, namespace.Name)
		}
	}

	if len(clusterNames) > 0 {
		err = d.client.DeleteNamespaces(ctx, clusterNames)
		if err != nil {
			return errors.Wrap(err, "failed delete namespaces")
		}
	}

	return nil
}

func (d *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	namespaceName, err := d.getClusterNamespace(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	service, err := d.client.GetService(ctx, namespaceName, CouchbaseClusterName+"-ui")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get service")
	}

	nodes, err := d.client.GetNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get nodes")
	}

	var externalIP string
	for _, node := range nodes.Items {
		for _, address := range node.Status.Addresses {
			// use the first IP we find
			externalIP = address.Address
			break
		}

		if externalIP != "" {
			break
		}
	}
	if externalIP == "" {
		return nil, errors.New("could not identify node IP to use")
	}

	var mgmtAddr string
	var mgmtTlsAddr string
	var connstr string
	var connstrTls string

	for _, port := range service.Spec.Ports {
		switch port.Name {
		case "couchbase-ui":
			mgmtAddr = fmt.Sprintf("http://%s:%d", externalIP, port.NodePort)
		case "couchbase-ui-tls":
			mgmtTlsAddr = fmt.Sprintf("https://%s:%d", externalIP, port.NodePort)
		case "data":
			connstr = fmt.Sprintf("couchbase://%s:%d", externalIP, port.NodePort)
		case "data-tls":
			connstrTls = fmt.Sprintf("couchbases://%s:%d", externalIP, port.NodePort)
		}
	}

	return &deployment.ConnectInfo{
		ConnStr:    connstr,
		ConnStrTls: connstrTls,
		Mgmt:       mgmtAddr,
		MgmtTls:    mgmtTlsAddr,
	}, nil
}

func (d *Deployer) Cleanup(ctx context.Context) error {
	curTime := time.Now()

	namespaces, err := d.client.ListNamespaces(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list namespaces")
	}

	var clusterNames []string
	for _, namespace := range namespaces.Items {
		if namespace.Labels["cbdc2.cluster_id"] != "" {
			expiryStr := namespace.Labels["cbdc2.expiry"]
			expiryTime, err := d.parseExpiry(expiryStr)
			if err != nil {
				d.logger.Debug("failed to parse cluster expiry time", zap.Error(err))
				continue
			}

			if !expiryTime.IsZero() && !expiryTime.After(curTime) {
				clusterNames = append(clusterNames, namespace.Name)
			}
		}
	}

	if len(clusterNames) > 0 {
		err = d.client.DeleteNamespaces(ctx, clusterNames)
		if err != nil {
			return errors.Wrap(err, "failed delete namespaces")
		}
	}

	return nil
}

func (d *Deployer) ListUsers(ctx context.Context, clusterID string) ([]deployment.UserInfo, error) {
	return nil, errors.New("caodeploy does not support listing users")
}

func (d *Deployer) CreateUser(ctx context.Context, clusterID string, opts *deployment.CreateUserOptions) error {
	return errors.New("caodeploy does not support creating users")
}

func (d *Deployer) DeleteUser(ctx context.Context, clusterID string, username string) error {
	return errors.New("caodeploy does not support deleting users")
}

func (d *Deployer) ListBuckets(ctx context.Context, clusterID string) ([]deployment.BucketInfo, error) {
	return nil, errors.New("caodeploy does not support listing buckets")
}

func (d *Deployer) CreateBucket(ctx context.Context, clusterID string, opts *deployment.CreateBucketOptions) error {
	return errors.New("caodeploy does not support creating buckets")
}

func (d *Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	return errors.New("caodeploy does not support deleting buckets")
}

func (d *Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("caodeploy does not support getting certificates")
}

func (d *Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error) {
	return "", errors.New("caodeploy does not support executing queries")
}

func (d *Deployer) ListCollections(ctx context.Context, clusterID string, bucketName string) ([]deployment.ScopeInfo, error) {
	return nil, errors.New("caodeploy does not support getting collections")
}

func (d *Deployer) CreateScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	return errors.New("caodeploy does not support creating scopes")
}

func (d *Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	return errors.New("caodeploy does not support creating collections")
}

func (d *Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	return errors.New("caodeploy does not support deleting scopes")
}

func (d *Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	return errors.New("caodeploy does not support deleting collections")
}

func (d *Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeID string, blockType deployment.BlockNodeTrafficType) error {
	return errors.New("caodeploy does not support traffic control")
}

func (d *Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("caodeploy does not support traffic control")
}

func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	return nil, errors.New("caodeploy does not support log collection")
}

func (d *Deployer) ListImages(ctx context.Context) ([]deployment.Image, error) {
	return nil, errors.New("caodeploy does not support image listing")
}

func (d *Deployer) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	return nil, errors.New("caodeploy does not support image search")
}

func (d *Deployer) PauseNode(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("caodeploy does not support node pausing")
}

func (d *Deployer) UnpauseNode(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("caodeploy does not support node pausing")
}
