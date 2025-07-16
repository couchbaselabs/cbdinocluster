package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	units "github.com/docker/go-units"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"k8s.io/utils/ptr"
)

type Controller struct {
	Logger      *zap.Logger
	DockerCli   *client.Client
	NetworkName string
}

type ContainerInfo struct {
	ContainerID          string
	Type                 string
	DnsName              string
	DnsSuffix            string
	NodeID               string
	ClusterID            string
	Name                 string
	Creator              string
	Owner                string
	Purpose              string
	Expiry               time.Time
	IPAddress            string
	InitialServerVersion string
	UsingDinoCerts       bool
}

func (c *Controller) parseContainerInfo(container container.Summary) *ContainerInfo {
	clusterID := container.Labels["com.couchbase.dyncluster.cluster_id"]
	nodeType := container.Labels["com.couchbase.dyncluster.type"]
	dnsName := container.Labels["com.couchbase.dyncluster.dns_name"]
	nodeID := container.Labels["com.couchbase.dyncluster.node_id"]
	nodeName := container.Labels["com.couchbase.dyncluster.node_name"]
	creator := container.Labels["com.couchbase.dyncluster.creator"]
	purpose := container.Labels["com.couchbase.dyncluster.purpose"]
	initialServerVersion := container.Labels["com.couchbase.dyncluster.initial_server_version"]
	usingDinoCerts := container.Labels["com.couchbase.dyncluster.using_dino_certs"]

	// If there is no cluster ID specified, this is not a cbdyncluster container
	if clusterID == "" {
		return nil
	}

	var pickedNetwork *network.EndpointSettings
	for _, network := range container.NetworkSettings.Networks {
		pickedNetwork = network
	}

	// if the node type is unspecified, we default to server-node
	if nodeType == "" {
		nodeType = "server-node"
	}

	var usingDinoCertsBool bool
	if usingDinoCerts != "" {
		usingDinoCertsBool = true
	}

	var dnsSuffix string
	if dnsName != "" {
		dnsParts := strings.SplitN(dnsName, ".", 2)
		if len(dnsParts) >= 2 {
			dnsSuffix = dnsParts[1]
		}
	}

	return &ContainerInfo{
		ContainerID:          container.ID,
		Type:                 nodeType,
		DnsName:              dnsName,
		DnsSuffix:            dnsSuffix,
		NodeID:               nodeID,
		ClusterID:            clusterID,
		Name:                 nodeName,
		Creator:              creator,
		Owner:                "",
		Purpose:              purpose,
		Expiry:               time.Time{},
		IPAddress:            pickedNetwork.IPAddress,
		InitialServerVersion: initialServerVersion,
		UsingDinoCerts:       usingDinoCertsBool,
	}
}

func (c *Controller) ListNodes(ctx context.Context) ([]*ContainerInfo, error) {
	c.Logger.Debug("listing nodes")

	containers, err := c.DockerCli.ContainerList(ctx, container.ListOptions{
		All: true,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list containers")
	}

	c.Logger.Debug("received initial container list, reading states")

	var nodes []*ContainerInfo

	for _, container := range containers {
		node := c.parseContainerInfo(container)
		if node != nil {
			nodeState, err := c.ReadNodeState(ctx, node.ContainerID)
			if err == nil && nodeState != nil {
				node.Expiry = nodeState.Expiry
			}

			nodes = append(nodes, node)
		}
	}

	return nodes, nil
}

type DockerNodeState struct {
	Expiry time.Time
}

type DockerNodeStateJson struct {
	Expiry time.Time
}

func (c *Controller) WriteNodeState(ctx context.Context, containerID string, state *DockerNodeState) error {
	c.Logger.Debug("writing node state", zap.String("container", containerID), zap.Any("state", state))

	jsonState := &DockerNodeStateJson{
		Expiry: state.Expiry,
	}

	jsonBytes, err := json.Marshal(jsonState)
	if err != nil {
		return errors.Wrap(err, "failed to marshal dyncluster node state")
	}

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "cbdyncluster/state",
		Size: int64(len(jsonBytes)),
	})
	tarFile.Write(jsonBytes)
	tarFile.Flush()

	err = c.DockerCli.CopyToContainer(ctx, containerID, "/var/", tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to write dyncluster node state")
	}

	return nil
}

func (c *Controller) ReadNodeState(ctx context.Context, containerID string) (*DockerNodeState, error) {
	c.Logger.Debug("reading node state", zap.String("container", containerID))

	resp, _, err := c.DockerCli.CopyFromContainer(ctx, containerID, "/var/cbdyncluster")
	if err != nil {
		return nil, errors.Wrap(err, "failed to read dyncluster node state")
	}

	var nodeStateJson *DockerNodeStateJson

	tarRdr := tar.NewReader(resp)
	for {
		tarHdr, err := tarRdr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, errors.Wrap(err, "failed to read dyncluster node state file")
		}

		if tarHdr.Name != "cbdyncluster/state" {
			continue
		}

		stateBytes, err := io.ReadAll(tarRdr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read dyncluster node state data")
		}

		err = json.Unmarshal(stateBytes, &nodeStateJson)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse dyncluster node state data")
		}
	}

	if nodeStateJson == nil {
		return nil, nil
	}

	return &DockerNodeState{
		Expiry: nodeStateJson.Expiry,
	}, nil
}

func (c *Controller) DeployS3MockNode(ctx context.Context, clusterID string, expiry time.Duration) (*ContainerInfo, error) {
	nodeID := "s3mock"
	logger := c.Logger.With(zap.String("nodeId", nodeID))

	logger.Debug("deploying s3mock node")

	_, err := MultiArchImagePuller{
		Logger:    c.Logger,
		DockerCli: c.DockerCli,
		ImagePath: "adobe/s3mock:latest",
	}.Pull(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull s3mock image")
	}

	containerName := "cbdynnode-s3-" + clusterID

	createResult, err := c.DockerCli.ContainerCreate(context.Background(), &container.Config{
		Image: "adobe/s3mock",
		Labels: map[string]string{
			"com.couchbase.dyncluster.cluster_id": clusterID,
			"com.couchbase.dyncluster.type":       "s3mock",
			"com.couchbase.dyncluster.purpose":    "s3mock backing for columnar",
			"com.couchbase.dyncluster.node_id":    nodeID,
		},
		// same effect as ntp
		Volumes: map[string]struct{}{"/etc/localtime:/etc/localtime": {}},
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: container.NetworkMode(c.NetworkName),
		CapAdd:      []string{"NET_ADMIN"},
		Resources: container.Resources{
			Ulimits: []*units.Ulimit{
				{Name: "nofile", Soft: 200000, Hard: 200000},
			},
		},
	}, nil, nil, containerName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create container")
	}

	containerID := createResult.ID

	logger.Debug("container created, starting", zap.String("container", containerID))

	err = c.DockerCli.ContainerStart(context.Background(), containerID, container.StartOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start container")
	}

	expiryTime := time.Time{}
	if expiry > 0 {
		expiryTime = time.Now().Add(expiry)
	}

	err = c.WriteNodeState(ctx, containerID, &DockerNodeState{
		Expiry: expiryTime,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed write node state")
	}

	// Cheap hack for simpler parsing...
	allNodes, err := c.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var node *ContainerInfo
	for _, allNode := range allNodes {
		if allNode.ContainerID == containerID {
			node = allNode
		}
	}
	if node == nil {
		return nil, errors.New("failed to find newly created container")
	}

	logger.Debug("container has started, waiting for it to get ready", zap.String("address", node.IPAddress))

	for {
		resp, err := http.Get(fmt.Sprintf("http://%s:%d", node.IPAddress, 9090))
		if err != nil || resp.StatusCode != 200 {
			logger.Debug("s3mock not ready yet", zap.Error(err))
			time.Sleep(500 * time.Millisecond)
			continue
		}

		break
	}

	logger.Debug("container is ready!")

	return node, nil
}

type ProxyTargetNode struct {
	Address               string
	IsEnterpriseAnalytics bool
}

func (c *Controller) DeployNginxNode(ctx context.Context, clusterID string, expiry time.Duration) (*ContainerInfo, error) {
	nodeID := "nginx"
	logger := c.Logger.With(zap.String("nodeId", nodeID))

	logger.Debug("deploying nginx node")

	_, err := MultiArchImagePuller{
		Logger:    c.Logger,
		DockerCli: c.DockerCli,
		ImagePath: "nginx:latest",
	}.Pull(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull nginx image")
	}

	containerName := "cbdynnode-nginx-" + clusterID

	createResult, err := c.DockerCli.ContainerCreate(context.Background(), &container.Config{
		Image: "nginx",
		Labels: map[string]string{
			"com.couchbase.dyncluster.cluster_id": clusterID,
			"com.couchbase.dyncluster.type":       "nginx",
			"com.couchbase.dyncluster.purpose":    "nginx backing for cluster",
			"com.couchbase.dyncluster.node_id":    nodeID,
		},
		// same effect as ntp
		Volumes: map[string]struct{}{"/etc/localtime:/etc/localtime": {}},
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: container.NetworkMode(c.NetworkName),
		CapAdd:      []string{"NET_ADMIN"},
		Resources: container.Resources{
			Ulimits: []*units.Ulimit{
				{Name: "nofile", Soft: 200000, Hard: 200000},
			},
		},
	}, nil, nil, containerName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create container")
	}

	containerID := createResult.ID

	logger.Debug("container created, starting", zap.String("container", containerID))

	err = c.DockerCli.ContainerStart(context.Background(), containerID, container.StartOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start container")
	}

	expiryTime := time.Time{}
	if expiry > 0 {
		expiryTime = time.Now().Add(expiry)
	}

	err = c.WriteNodeState(ctx, containerID, &DockerNodeState{
		Expiry: expiryTime,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed write node state")
	}

	// Cheap hack for simpler parsing...
	allNodes, err := c.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var node *ContainerInfo
	for _, allNode := range allNodes {
		if allNode.ContainerID == containerID {
			node = allNode
		}
	}
	if node == nil {
		return nil, errors.New("failed to find newly created container")
	}

	logger.Debug("container has started, waiting for it to get ready", zap.String("address", node.IPAddress))

	for {
		resp, err := http.Get(fmt.Sprintf("http://%s:%d", node.IPAddress, 80))
		if err != nil || resp.StatusCode != 200 {
			logger.Debug("nginx not ready yet", zap.Error(err))
			time.Sleep(100 * time.Millisecond)
			continue
		}

		break
	}

	logger.Debug("container is ready!")

	return node, nil
}

func (c *Controller) DeployHaproxyNode(ctx context.Context, clusterID string, expiry time.Duration) (*ContainerInfo, error) {
	nodeID := "haproxy"
	logger := c.Logger.With(zap.String("nodeId", nodeID))

	logger.Debug("deploying haproxy node")

	_, err := MultiArchImagePuller{
		Logger:    c.Logger,
		DockerCli: c.DockerCli,
		ImagePath: "haproxy:latest",
	}.Pull(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull haproxy image")
	}

	containerName := "cbdynnode-haproxy-" + clusterID

	createResult, err := c.DockerCli.ContainerCreate(context.Background(), &container.Config{
		Image: "haproxy",
		Labels: map[string]string{
			"com.couchbase.dyncluster.cluster_id": clusterID,
			"com.couchbase.dyncluster.type":       "haproxy",
			"com.couchbase.dyncluster.purpose":    "haproxy backing for cluster",
			"com.couchbase.dyncluster.node_id":    nodeID,
		},
		// same effect as ntp
		Volumes: map[string]struct{}{"/etc/localtime:/etc/localtime": {}},
	}, &container.HostConfig{
		// AutoRemove:  true,
		NetworkMode: container.NetworkMode(c.NetworkName),
		CapAdd:      []string{"NET_ADMIN"},
		Resources: container.Resources{
			Ulimits: []*units.Ulimit{
				{Name: "nofile", Soft: 200000, Hard: 200000},
			},
		},
	}, nil, nil, containerName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create container")
	}

	containerID := createResult.ID

	logger.Debug("container created, creating base config", zap.String("container", containerID))

	configBytes := []byte("frontend myfrontend\n  mode http\n  bind :80\n")

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "haproxy/haproxy.cfg",
		Size: int64(len(configBytes)),
		Mode: 0666,
	})
	tarFile.Write(configBytes)
	tarFile.Flush()

	err = c.DockerCli.CopyToContainer(ctx, containerID, "/usr/local/etc", tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to store base haproxy config")
	}

	logger.Debug("container config stored, starting", zap.String("container", containerID))

	err = c.DockerCli.ContainerStart(context.Background(), containerID, container.StartOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start container")
	}

	expiryTime := time.Time{}
	if expiry > 0 {
		expiryTime = time.Now().Add(expiry)
	}

	err = c.WriteNodeState(ctx, containerID, &DockerNodeState{
		Expiry: expiryTime,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed write node state")
	}

	// Cheap hack for simpler parsing...
	allNodes, err := c.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var node *ContainerInfo
	for _, allNode := range allNodes {
		if allNode.ContainerID == containerID {
			node = allNode
		}
	}
	if node == nil {
		return nil, errors.New("failed to find newly created container")
	}

	logger.Debug("container has started, waiting for it to get ready", zap.String("address", node.IPAddress))

	for {
		resp, err := http.Get(fmt.Sprintf("http://%s:%d", node.IPAddress, 80))
		if err != nil || resp.StatusCode != 503 {
			logger.Debug("haproxy not ready yet", zap.Error(err))
			time.Sleep(100 * time.Millisecond)
			continue
		}

		break
	}

	logger.Debug("container is ready!")

	return node, nil
}

func (c *Controller) UpdateHaproxyCertificates(ctx context.Context, containerID string, certPem []byte, keyPem []byte) error {
	c.Logger.Debug("uploading haproxy certificates",
		zap.String("container", containerID),
		zap.Int("certLen", len(certPem)),
		zap.Int("keyLen", len(keyPem)))

	concatPem := append(certPem, keyPem...)

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "cert.pem",
		Size: int64(len(concatPem)),
		Mode: 0666,
	})
	tarFile.Write(concatPem)
	tarFile.Flush()

	err := c.DockerCli.CopyToContainer(ctx, containerID, "/usr/local/etc/haproxy/", tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to write certificates")
	}

	return nil
}

func (c *Controller) UpdateHaproxyConfig(
	ctx context.Context,
	containerID string,
	targets []ProxyTargetNode,
	enableSsl,
	isColumnar bool,
) error {
	// this is configured to broadly match AWS Network Load Balancer defaults
	maxRetrys := 0
	connectTimeout := "350s"
	clientTimeout := "350s"
	serverTimeout := "350s"
	checkConfig := "check inter 30s fall 2 rise 5"

	c.Logger.Debug("writing haproxy config", zap.String("container", containerID), zap.Any("targets", targets))

	var haConf string

	haConf += "defaults\n"
	haConf += "  mode http\n"
	haConf += fmt.Sprintf("  retries %d\n", maxRetrys)
	haConf += fmt.Sprintf("  timeout connect %s\n", connectTimeout)
	haConf += fmt.Sprintf("  timeout client %s\n", clientTimeout)
	haConf += fmt.Sprintf("  timeout server %s\n", serverTimeout)
	haConf += "\n"

	haConf += "backend static_backend\n"
	haConf += `  http-request return status 200 content-type "text/plain" lf-string "HAProxy"` + "\n"
	haConf += "\n"

	writeForwardedPort := func(port int, stickySession, withSsl bool) {
		healthPath := "/"
		switch port {
		case 8091, 18091:
			healthPath = "/whoami"
		case 8092, 18092:
			healthPath = "/"
		case 8093, 18093:
			healthPath = "/admin/ping"
		case 8094, 18094:
			healthPath = "/api/ping"
		case 8095, 18095:
			healthPath = "/admin/ping"
			for _, target := range targets {
				if target.IsEnterpriseAnalytics {
					healthPath = "/api/v1/health"
					break
				}
			}
		}

		haConf += fmt.Sprintf("frontend frontend%d\n", port)
		if withSsl {
			haConf += fmt.Sprintf("  bind :%d ssl crt %s\n", port, "/usr/local/etc/haproxy/cert.pem")
		} else {
			haConf += fmt.Sprintf("  bind :%d\n", port)
		}
		haConf += fmt.Sprintf("  default_backend backend%d\n", port)
		haConf += "\n"
		haConf += fmt.Sprintf("backend backend%d\n", port)
		if !stickySession {
			haConf += "  balance roundrobin\n"
		} else {
			haConf += "  balance source\n"
		}
		haConf += fmt.Sprintf("  option httpchk GET %s\n", healthPath)
		for i, target := range targets {
			sslConfig := ""
			if withSsl {
				sslConfig = "ssl verify none"
			}

			haConf += fmt.Sprintf("  server node%d %s:%d %s %s\n", i, target.Address, port, sslConfig, checkConfig)
		}
		haConf += "\n"
	}

	writeForwardedPort(8091, true, false)
	writeForwardedPort(8092, false, false)
	writeForwardedPort(8093, false, false)
	writeForwardedPort(8094, false, false)
	writeForwardedPort(8095, false, false)

	if enableSsl {
		writeForwardedPort(18091, true, true)
		writeForwardedPort(18092, false, true)
		writeForwardedPort(18093, false, true)
		writeForwardedPort(18094, false, true)
		writeForwardedPort(18095, false, true)
	}

	haConf += "frontend frontend80\n"
	haConf += "  bind :80\n"
	haConf += "  stats enable\n"
	haConf += "  stats refresh 10s\n"
	haConf += "  stats uri /stats\n"
	haConf += "  stats show-modules\n"
	haConf += "  stats admin if TRUE\n"
	if isColumnar {
		haConf += `  http-request add-header X-Analytics "1" if { path_beg /analytics }` + "\n"
		haConf += `  http-request replace-path ^/analytics(?:/)?(.*) /\1 if { hdr_cnt(X-Analytics) gt 0 }` + "\n"
		haConf += "  use_backend backend8095 if { hdr_cnt(X-Analytics) gt 0 }\n"
	}
	haConf += "  default_backend static_backend\n"
	haConf += "\n"

	if enableSsl {
		haConf += "frontend frontend443\n"
		haConf += "  bind :443 ssl crt /usr/local/etc/haproxy/cert.pem\n"
		if isColumnar {
			haConf += `  http-request add-header X-Analytics "1" if { path_beg /analytics }` + "\n"
			haConf += `  http-request replace-path ^/analytics(?:/)?(.*) /\1 if { hdr_cnt(X-Analytics) gt 0 }` + "\n"
			haConf += "  use_backend backend8095 if { hdr_cnt(X-Analytics) gt 0 }\n"
		}
		haConf += "  default_backend static_backend\n"
		haConf += "\n"
	}

	confBytes := []byte(haConf)

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "haproxy.cfg",
		Size: int64(len(confBytes)),
		Mode: 0666,
	})
	tarFile.Write(confBytes)
	tarFile.Flush()

	err := c.DockerCli.CopyToContainer(ctx, containerID, "/usr/local/etc/haproxy/", tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to write nginx config")
	}

	err = c.DockerCli.ContainerKill(ctx, containerID, "HUP")
	if err != nil {
		return errors.Wrap(err, "failed to reload haproxy config")
	}

	return nil
}

func (c *Controller) UpdateNginxCertificates(ctx context.Context, containerID string, certPem []byte, keyPem []byte) error {
	c.Logger.Debug("uploading nginx certificates",
		zap.String("container", containerID),
		zap.Int("certLen", len(certPem)),
		zap.Int("keyLen", len(keyPem)))

	err := c.execCmd(ctx, containerID, []string{"mkdir", "-p", "/etc/nginx/ssl/"})
	if err != nil {
		return errors.Wrap(err, "failed to mkdir nginx ssl folder")
	}

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "cert.pem",
		Size: int64(len(certPem)),
	})
	tarFile.Write(certPem)
	tarFile.WriteHeader(&tar.Header{
		Name: "key.pem",
		Size: int64(len(keyPem)),
	})
	tarFile.Write(keyPem)
	tarFile.Flush()

	err = c.DockerCli.CopyToContainer(ctx, containerID, "/etc/nginx/ssl/", tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to write certificates")
	}

	return nil
}

func (c *Controller) UpdateNginxConfig(ctx context.Context, containerID string, targets []ProxyTargetNode, enableSsl, isColumnar bool) error {
	c.Logger.Debug("writing nginx config", zap.String("container", containerID), zap.Any("targets", targets))

	var nginxConf string
	writePortMapping := func(listenPort, targetPort int, stickySession, withSsl bool, path string) {
		if len(targets) == 0 {
			return
		}
		if path == "" {
			path = "/"
		} else if path[len(path)-1] != '/' {
			path += "/"
		}

		upstreamName := fmt.Sprintf("backend%d%s", listenPort, strings.ReplaceAll(path, "/", "_"))
		nginxConf += fmt.Sprintf("upstream %s {\n", upstreamName)
		if stickySession {
			nginxConf += "    ip_hash;\n"
		}
		for _, target := range targets {
			nginxConf += fmt.Sprintf("    server %s:%d;\n", target.Address, targetPort)
		}
		nginxConf += "}\n"

		nginxConf += "server {\n"
		if withSsl {
			nginxConf += fmt.Sprintf("    listen %d ssl;\n", listenPort)
			nginxConf += "    ssl_certificate /etc/nginx/ssl/cert.pem;\n"
			nginxConf += "    ssl_certificate_key /etc/nginx/ssl/key.pem;\n"
		} else {
			nginxConf += fmt.Sprintf("    listen %d;\n", listenPort)
		}

		nginxConf += fmt.Sprintf("    location %s {\n", path)
		protocol := "http"
		if withSsl {
			protocol = "https"
		}
		nginxConf += fmt.Sprintf("        proxy_pass %s://%s/;\n", protocol, upstreamName)
		nginxConf += "        proxy_set_header Host $http_host;\n"
		nginxConf += "        proxy_set_header X-Real-IP $remote_addr;\n"
		nginxConf += "        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n"
		nginxConf += "    }\n"
		nginxConf += "}\n"
	}

	writeForwardedPort := func(portInt int, stickySession bool, withSsl bool) {
		writePortMapping(portInt, portInt, stickySession, withSsl, "")
	}

	writeForwardedPort(8091, true, false)
	writeForwardedPort(8092, false, false)
	writeForwardedPort(8093, false, false)
	writeForwardedPort(8094, false, false)
	writeForwardedPort(8095, false, false)
	writeForwardedPort(8096, false, false)
	writeForwardedPort(8097, false, false)

	if isColumnar {
		writePortMapping(80, 8095, false, false, "/analytics")
	}

	if enableSsl {
		writeForwardedPort(18091, true, true)
		writeForwardedPort(18092, false, true)
		writeForwardedPort(18093, false, true)
		writeForwardedPort(18094, false, true)
		writeForwardedPort(18095, false, true)
		writeForwardedPort(18096, false, true)
		writeForwardedPort(18097, false, true)
		if isColumnar {
			writePortMapping(443, 18095, false, true, "/analytics")
		}
	}

	confBytes := []byte(nginxConf)

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "cb.conf",
		Size: int64(len(confBytes)),
	})
	tarFile.Write(confBytes)
	tarFile.Flush()

	err := c.DockerCli.CopyToContainer(ctx, containerID, "/etc/nginx/conf.d/", tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to write nginx config")
	}

	err = c.execCmd(ctx, containerID, []string{"nginx", "-s", "reload"})
	if err != nil {
		return errors.Wrap(err, "failed to reload nginx config")
	}

	return nil
}

func (c *Controller) UploadCertificates(
	ctx context.Context,
	containerID string,
	nodeCertPem []byte,
	nodeKeyPem []byte,
	caPems [][]byte,
) error {
	c.Logger.Debug("uploading couchbase certificates",
		zap.String("container", containerID),
		zap.Int("nodeCertLen", len(nodeCertPem)),
		zap.Int("nodeKeyLen", len(nodeKeyPem)),
		zap.Int("numCaPems", len(caPems)))

	var installLocation string
	// Check for /opt/couchbase directory
	err := c.execCmd(ctx, containerID, []string{"test", "-d", "/opt/couchbase"})
	if err == nil {
		installLocation = "couchbase"
	} else {
		// Check for /opt/enterprise-analytics directory
		err := c.execCmd(ctx, containerID, []string{"test", "-d", "/opt/enterprise-analytics"})
		if err == nil {
			installLocation = "enterprise-analytics"
		} else {
			return errors.New("neither /opt/couchbase nor /opt/enterprise-analytics directory found")
		}
	}

	c.Logger.Debug("detected installation location", zap.String("location", installLocation))

	inboxPath := fmt.Sprintf("/opt/%s/var/lib/couchbase/inbox/", installLocation)

	err = c.execCmd(ctx, containerID, []string{"mkdir", "-p", inboxPath})
	if err != nil {
		return errors.Wrapf(err, "failed to mkdir inbox directory at %s", inboxPath)
	}

	tarBuf := bytes.NewBuffer(nil)
	tarFile := tar.NewWriter(tarBuf)
	tarFile.WriteHeader(&tar.Header{
		Name: "chain.pem",
		Size: int64(len(nodeCertPem)),
	})
	tarFile.Write(nodeCertPem)
	tarFile.WriteHeader(&tar.Header{
		Name: "pkey.key",
		Size: int64(len(nodeKeyPem)),
	})
	tarFile.Write(nodeKeyPem)
	caPemLen := 0
	for _, caPem := range caPems {
		caPemLen += len(caPem)
	}
	tarFile.WriteHeader(&tar.Header{
		Name: "CA/ca.pem",
		Size: int64(caPemLen),
	})
	for _, caPem := range caPems {
		tarFile.Write(caPem)
	}
	tarFile.Flush()

	err = c.DockerCli.CopyToContainer(ctx, containerID, inboxPath, tarBuf, container.CopyToContainerOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to write certificates")
	}

	err = c.execCmd(ctx, containerID, []string{"chown", "-R", "couchbase", inboxPath})
	if err != nil {
		return errors.Wrap(err, "failed to chown couchbase inbox")
	}

	err = c.execCmd(ctx, containerID, []string{"chmod", "-R", "0700", inboxPath})
	if err != nil {
		return errors.Wrap(err, "failed to chmod couchbase inbox")
	}

	return nil
}

type DeployNodeOptions struct {
	Purpose            string
	Expiry             time.Duration
	ClusterID          string
	Image              *ImageRef
	ImageServerVersion string
	IsColumnar         bool
	DnsSuffix          string
	EnvVars            map[string]string
	UseDinoCerts       bool
}

func (c *Controller) DeployNode(ctx context.Context, def *DeployNodeOptions) (*ContainerInfo, error) {
	nodeID := uuid.NewString()
	logger := c.Logger.With(zap.String("nodeId", nodeID))

	logger.Debug("deploying node", zap.Any("def", def))

	containerName := "cbdynnode-" + nodeID

	dnsName := ""
	if def.DnsSuffix != "" {
		dnsName = fmt.Sprintf("%s.%s", nodeID[:6], def.DnsSuffix)
	}

	var envVars []string
	for varName, varValue := range def.EnvVars {
		envVars = append(envVars, fmt.Sprintf("%s=%s", varName, varValue))
	}

	nodeType := "server-node"
	if def.IsColumnar {
		nodeType = "columnar-node"
	}

	usingDinoCerts := ""
	if def.UseDinoCerts {
		usingDinoCerts = "true"
	}

	createResult, err := c.DockerCli.ContainerCreate(context.Background(), &container.Config{
		Image: def.Image.ImagePath,
		Labels: map[string]string{
			"com.couchbase.dyncluster.cluster_id":             def.ClusterID,
			"com.couchbase.dyncluster.type":                   nodeType,
			"com.couchbase.dyncluster.dns_name":               dnsName,
			"com.couchbase.dyncluster.purpose":                def.Purpose,
			"com.couchbase.dyncluster.node_id":                nodeID,
			"com.couchbase.dyncluster.initial_server_version": def.ImageServerVersion,
			"com.couchbase.dyncluster.using_dino_certs":       usingDinoCerts,
		},
		// same effect as ntp
		Volumes: map[string]struct{}{"/etc/localtime:/etc/localtime": {}},
		Env:     envVars,
	}, &container.HostConfig{
		AutoRemove:  true,
		NetworkMode: container.NetworkMode(c.NetworkName),
		CapAdd:      []string{"NET_ADMIN"},
		Resources: container.Resources{
			Ulimits: []*units.Ulimit{
				{Name: "nofile", Soft: 200000, Hard: 200000},
			},
		},
	}, nil, nil, containerName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create container")
	}

	containerID := createResult.ID

	logger.Debug("container created, starting", zap.String("container", containerID))

	err = c.DockerCli.ContainerStart(context.Background(), containerID, container.StartOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start container")
	}

	expiryTime := time.Time{}
	if def.Expiry > 0 {
		expiryTime = time.Now().Add(def.Expiry)
	}

	err = c.WriteNodeState(ctx, containerID, &DockerNodeState{
		Expiry: expiryTime,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed write node state")
	}

	// Cheap hack for simpler parsing...
	allNodes, err := c.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var node *ContainerInfo
	for _, allNode := range allNodes {
		if allNode.ContainerID == containerID {
			node = allNode
		}
	}
	if node == nil {
		return nil, errors.New("failed to find newly created container")
	}

	logger.Debug("container has started, waiting for it to get ready", zap.String("address", node.IPAddress))

	clusterCtrl := &clustercontrol.NodeManager{
		Logger:   c.Logger,
		Endpoint: fmt.Sprintf("http://%s:%d", node.IPAddress, 8091),
	}

	err = clusterCtrl.WaitForOnline(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for node readiness")
	}

	logger.Debug("container is ready!")

	return node, nil
}

func (c *Controller) RemoveNode(ctx context.Context, containerID string) error {
	logger := c.Logger.With(zap.String("container", containerID))
	logger.Debug("removing node")

	logger.Debug("stopping container")

	err := c.DockerCli.ContainerStop(ctx, containerID, container.StopOptions{
		Timeout: ptr.To(0),
	})
	if err != nil {
		return errors.Wrap(err, "failed to stop container")
	}

	logger.Debug("removing container")

	// we try to call remove to force it to be removed
	err = c.DockerCli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to remove container")
	}

	logger.Debug("waiting for container to disappear")

	// We call this to 'wait' for the removal to finish...
	for {
		nodes, err := c.ListNodes(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to list nodes")
		}

		foundNode := false
		for _, node := range nodes {
			if node.ContainerID == containerID {
				foundNode = true
			}
		}

		if foundNode {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		break
	}

	logger.Debug("node has been removed!")

	return nil
}

func (c *Controller) UpdateExpiry(ctx context.Context, containerID string, newExpiryTime time.Time) error {
	state, err := c.ReadNodeState(ctx, containerID)
	if err != nil {
		return errors.Wrap(err, "failed read existing node state")
	}

	state.Expiry = newExpiryTime

	err = c.WriteNodeState(ctx, containerID, state)
	if err != nil {
		return errors.Wrap(err, "failed write updated node state")
	}

	return nil
}

func (c *Controller) execCmd(ctx context.Context, containerID string, cmd []string) error {
	c.Logger.Debug("executing cmd",
		zap.String("containerID", containerID),
		zap.Strings("cmd", cmd))

	return dockerExecAndPipe(ctx, c.Logger, c.DockerCli, containerID, cmd)
}

func (c *Controller) execIptables(ctx context.Context, containerID string, args []string) error {
	err := c.execCmd(ctx, containerID, append([]string{"iptables"}, args...))
	if err != nil {
		// if the iptables command fails initially, we attempt to install iptables first
		c.Logger.Debug("failed to execute iptables, attempting to install")

		err := c.execCmd(ctx, containerID, []string{"apt-get", "update"})
		if err != nil {
			return errors.Wrap(err, "failed to update apt")
		}

		err = c.execCmd(ctx, containerID, []string{"apt-get", "-y", "install", "iptables"})
		if err != nil {
			return errors.Wrap(err, "failed to install iptables")
		}

		// try it again after installing iptables
		err = c.execCmd(ctx, containerID, append([]string{"iptables"}, args...))
		if err != nil {
			return errors.Wrap(err, "failed to execute iptables command")
		}
	}

	return nil
}

type TrafficControlType string

const (
	TrafficControlBlockNodes   TrafficControlType = "nodes"
	TrafficControlBlockClients TrafficControlType = "clients"
	TrafficControlBlockAll     TrafficControlType = "all"
	TrafficControlAllowAll     TrafficControlType = "none"
)

func (c *Controller) SetTrafficControl(
	ctx context.Context,
	containerID string,
	tcType TrafficControlType,
	blockType string,
	extraBlocked []string,
	extraAllowed []string,
) error {
	logger := c.Logger.With(zap.String("container", containerID))
	logger.Debug("setting up traffic control",
		zap.String("blockType", string(tcType)))

	if tcType == TrafficControlAllowAll && len(extraBlocked) == 0 {
		// if there are no extra blocked ips, and we are just allowing all traffic,
		// we can skip iptables setup when its not installed.
		err := c.execCmd(ctx, containerID, []string{"iptables", "-F"})
		if err != nil {
			logger.Debug("failed to clear iptables, this probably just means it never had rules set",
				zap.Error(err))
		}

		return nil
	}

	netInfo, err := c.DockerCli.NetworkInspect(ctx, c.NetworkName, network.InspectOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to inspect network")
	}

	if len(netInfo.IPAM.Config) < 1 {
		return errors.New("more than one ipam config, cannot identify node subnet")
	}
	ipamConfig := netInfo.IPAM.Config[0]

	gatewayIP := ipamConfig.Gateway
	ipRange := ipamConfig.Subnet
	if ipamConfig.IPRange != "" {
		ipRange = ipamConfig.IPRange
	}

	if ipRange == "" || gatewayIP == "" {
		return errors.New("failed to identify subnet or gateway ip")
	}

	err = c.execIptables(ctx, containerID, []string{"-F"})
	if err != nil {
		return errors.Wrap(err, "failed to clear iptables")
	}

	iptableAllowAll := func(table string) error {
		return c.execIptables(ctx, containerID, []string{"-A", table, "-j", "ACCEPT"})
	}
	iptableAllow := func(table string, cidr string) error {
		return c.execIptables(ctx, containerID, []string{"-A", table, "-s", cidr, "-j", "ACCEPT"})
	}
	iptableBlockAll := func(table string) error {
		if blockType == "" {
			return c.execIptables(ctx, containerID, []string{"-A", table, "-j", "DROP"})
		} else if blockType == "tcp-reset" {
			err := c.execIptables(ctx, containerID, []string{"-A", table, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"})
			if err != nil {
				return err
			}

			return c.execIptables(ctx, containerID, []string{"-A", table, "-j", "REJECT"})
		} else {
			return c.execIptables(ctx, containerID, []string{"-A", table, "-j", "REJECT", "--reject-with", blockType})
		}
	}
	iptableBlock := func(table string, cidr string) error {
		if blockType == "" {
			return c.execIptables(ctx, containerID, []string{"-A", table, "-s", cidr, "-j", "DROP"})
		} else if blockType == "tcp-reset" {
			err := c.execIptables(ctx, containerID, []string{"-A", table, "-s", cidr, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"})
			if err != nil {
				return err
			}

			return c.execIptables(ctx, containerID, []string{"-A", table, "-s", cidr, "-j", "REJECT"})
		} else {
			return c.execIptables(ctx, containerID, []string{"-A", table, "-s", cidr, "-j", "REJECT", "--reject-with", blockType})
		}
	}

	// add extra allowed ips
	for _, allowedIP := range extraAllowed {
		err = iptableAllow("INPUT", allowedIP)
		if err != nil {
			return errors.Wrapf(err, "failed to create INPUT iptables rule for allowed ip %s", allowedIP)
		}

		err = iptableAllow("OUTPUT", allowedIP)
		if err != nil {
			return errors.Wrapf(err, "failed to create OUTPUT iptables rule for allowed ip %s", allowedIP)
		}
	}

	// add extra blocked ips
	for _, blockedIP := range extraBlocked {
		err = iptableBlock("INPUT", blockedIP)
		if err != nil {
			return errors.Wrapf(err, "failed to create INPUT iptables rule for blocked ip %s", blockedIP)
		}

		err = iptableBlock("OUTPUT", blockedIP)
		if err != nil {
			return errors.Wrapf(err, "failed to create OUTPUT iptables rule for blocked ip %s", blockedIP)
		}
	}

	err = iptableAllowAll("OUTPUT")
	if err != nil {
		return errors.Wrapf(err, "failed to create OUTPUT iptables rule to accept all traffic")
	}

	if tcType == TrafficControlBlockNodes {
		// always accept from the gateway
		err = iptableAllow("INPUT", gatewayIP)
		if err != nil {
			return errors.Wrap(err, "failed to create INPUT iptables rule to allow gateway traffic")
		}
		err = iptableAllow("OUTPUT", gatewayIP)
		if err != nil {
			return errors.Wrap(err, "failed to create OUTPUT iptables rule to allow gateway traffic")
		}

		// reject from the rest of that subnet
		err = iptableBlock("INPUT", ipRange)
		if err != nil {
			return errors.Wrap(err, "failed to create INPUT iptables rule to block inter-node traffic")
		}
		err = iptableBlock("OUTPUT", ipRange)
		if err != nil {
			return errors.Wrap(err, "failed to create OUTPUT iptables rule to block inter-node traffic")
		}
	} else if tcType == TrafficControlBlockClients {
		// always reject from the gateway
		err = iptableBlock("INPUT", gatewayIP)
		if err != nil {
			return errors.Wrap(err, "failed to create INPUT iptables rule to block gateway traffic")
		}

		// always accept from inter-node subnet
		err = iptableAllow("INPUT", ipRange)
		if err != nil {
			return errors.Wrap(err, "failed to create INPUT iptables rule to allow inter-node traffic")
		}

		// block everyone else
		err = iptableBlockAll("INPUT")
		if err != nil {
			return errors.Wrap(err, "failed to create INPUT iptables rule to drop all traffic")
		}
	} else if tcType == TrafficControlBlockAll {
		// block all packets
		err = iptableBlockAll("INPUT")
		if err != nil {
			return errors.Wrap(err, "failed to create INPUT iptables rule to drop all traffic")
		}
	} else if tcType == TrafficControlAllowAll {
		// nothing to do, we are allowing all traffic
	} else {
		return errors.New("invalid traffic control type")
	}

	err = c.execIptables(ctx, containerID, []string{"-S"})
	if err != nil {
		c.Logger.Debug("failed to print iptables state", zap.Error(err))
	}

	logger.Debug("traffic control has been set up!")

	return nil
}
