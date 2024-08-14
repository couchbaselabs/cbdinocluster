package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	units "github.com/docker/go-units"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Controller struct {
	Logger      *zap.Logger
	DockerCli   *client.Client
	NetworkName string
}

type NodeInfo struct {
	ContainerID          string
	Type                 string
	NodeID               string
	ClusterID            string
	Name                 string
	Creator              string
	Owner                string
	Purpose              string
	Expiry               time.Time
	IPAddress            string
	InitialServerVersion string
}

func (c *Controller) parseContainerInfo(container types.Container) *NodeInfo {
	clusterID := container.Labels["com.couchbase.dyncluster.cluster_id"]
	nodeType := container.Labels["com.couchbase.dyncluster.type"]
	nodeID := container.Labels["com.couchbase.dyncluster.node_id"]
	nodeName := container.Labels["com.couchbase.dyncluster.node_name"]
	creator := container.Labels["com.couchbase.dyncluster.creator"]
	purpose := container.Labels["com.couchbase.dyncluster.purpose"]
	initialServerVersion := container.Labels["com.couchbase.dyncluster.initial_server_version"]

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

	return &NodeInfo{
		ContainerID:          container.ID,
		Type:                 nodeType,
		NodeID:               nodeID,
		ClusterID:            clusterID,
		Name:                 nodeName,
		Creator:              creator,
		Owner:                "",
		Purpose:              purpose,
		Expiry:               time.Time{},
		IPAddress:            pickedNetwork.IPAddress,
		InitialServerVersion: initialServerVersion,
	}
}

func (c *Controller) ListNodes(ctx context.Context) ([]*NodeInfo, error) {
	c.Logger.Debug("listing nodes")

	containers, err := c.DockerCli.ContainerList(ctx, types.ContainerListOptions{
		All: true,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list containers")
	}

	c.Logger.Debug("received initial container list, reading states")

	var nodes []*NodeInfo

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

	err = c.DockerCli.CopyToContainer(ctx, containerID, "/var/", tarBuf, types.CopyToContainerOptions{})
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

func (c *Controller) DeployS3MockNode(ctx context.Context, clusterID string, expiry time.Duration) (*NodeInfo, error) {
	nodeID := "s3mock"
	logger := c.Logger.With(zap.String("nodeId", nodeID))

	logger.Debug("deploying s3mock node")

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

	err = c.DockerCli.ContainerStart(context.Background(), containerID, types.ContainerStartOptions{})
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

	var node *NodeInfo
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
			time.Sleep(100 * time.Millisecond)
			continue
		}

		break
	}

	logger.Debug("container is ready!")

	return node, nil
}

type DeployNodeOptions struct {
	Purpose            string
	Expiry             time.Duration
	ClusterID          string
	Image              *ImageRef
	ImageServerVersion string
	IsColumnar         bool
	EnvVars            map[string]string
}

func (c *Controller) DeployNode(ctx context.Context, def *DeployNodeOptions) (*NodeInfo, error) {
	nodeID := uuid.NewString()
	logger := c.Logger.With(zap.String("nodeId", nodeID))

	logger.Debug("deploying node", zap.Any("def", def))

	containerName := "cbdynnode-" + nodeID

	var envVars []string
	for varName, varValue := range def.EnvVars {
		envVars = append(envVars, fmt.Sprintf("%s=%s", varName, varValue))
	}

	nodeType := "server-node"
	if def.IsColumnar {
		nodeType = "columnar-node"
	}

	createResult, err := c.DockerCli.ContainerCreate(context.Background(), &container.Config{
		Image: def.Image.ImagePath,
		Labels: map[string]string{
			"com.couchbase.dyncluster.cluster_id":             def.ClusterID,
			"com.couchbase.dyncluster.type":                   nodeType,
			"com.couchbase.dyncluster.purpose":                def.Purpose,
			"com.couchbase.dyncluster.node_id":                nodeID,
			"com.couchbase.dyncluster.initial_server_version": def.ImageServerVersion,
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

	err = c.DockerCli.ContainerStart(context.Background(), containerID, types.ContainerStartOptions{})
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

	var node *NodeInfo
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

	err := c.DockerCli.ContainerStop(ctx, containerID, container.StopOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to stop container")
	}

	logger.Debug("removing container")

	// we try to call remove to force it to be removed
	c.DockerCli.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{})

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

func (c *Controller) SetTrafficControl(ctx context.Context, containerID string, tcType TrafficControlType) error {
	logger := c.Logger.With(zap.String("container", containerID))
	logger.Debug("setting up traffic control",
		zap.String("blockType", string(tcType)))

	netInfo, err := c.DockerCli.NetworkInspect(ctx, c.NetworkName, types.NetworkInspectOptions{})
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

	if tcType == TrafficControlBlockNodes {
		// reject from the rest of that subnet
		err = c.execIptables(ctx, containerID, []string{"-I", "INPUT", "-s", ipRange, "-j", "DROP"})
		if err != nil {
			return errors.Wrap(err, "failed to create iptables rule")
		}

		// always accept from the gateway
		err = c.execIptables(ctx, containerID, []string{"-I", "INPUT", "-s", gatewayIP, "-j", "ACCEPT"})
		if err != nil {
			return errors.Wrap(err, "failed to create iptables rule")
		}
	} else if tcType == TrafficControlBlockClients {
		// block everyone else
		err = c.execIptables(ctx, containerID, []string{"-I", "INPUT", "-j", "DROP"})
		if err != nil {
			return errors.Wrap(err, "failed to create iptables rule")
		}

		// always accept from inter-node subnet
		err = c.execIptables(ctx, containerID, []string{"-I", "INPUT", "-s", ipRange, "-j", "ACCEPT"})
		if err != nil {
			return errors.Wrap(err, "failed to create iptables rule")
		}

		// always reject from the gateway
		err = c.execIptables(ctx, containerID, []string{"-I", "INPUT", "-s", gatewayIP, "-j", "DROP"})
		if err != nil {
			return errors.Wrap(err, "failed to create iptables rule")
		}
	} else if tcType == TrafficControlBlockAll {
		// block all packets
		err = c.execIptables(ctx, containerID, []string{"-I", "INPUT", "-j", "DROP"})
		if err != nil {
			return errors.Wrap(err, "failed to create iptables rule")
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
