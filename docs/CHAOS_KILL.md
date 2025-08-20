## Chaos kill-couchbase command

Cbdinocluster provides a way to kill couchbase service running
on a docker container by using `chaos kill-couchbase` command.
This method is used to simulate a stop and restart of the couchbase server.

### What it does

If no nodeId is provided, the command will terminate the Couchbase service on all nodes in the cluster.
If a nodeId is specified, only the Couchbase service on the specified node will be terminated.
The command should first gather information about the nodes, including the containers on which they are running,
And it should then simply exec into a Docker container and run `pkill -f couchbase-server`
to terminate the Couchbase service.

The `runSv` supervisor will automatically restart couchbase-server within a few seconds.

### Usage

This command is useful in case where a Couchbase server may be unexpectedly stopped and restarted.
It allows observing behavior during the transition period when the server is restarting,
which can reveal issues with connection handling, retries, and error recovery.

### Kill Couchbase Service

```
cbdinocluster chaos kill-couchbase <clusterId> [nodeId]
```

### Supported Deployers

| Deployer | Supported | Notes                   |
|----------|-----------|-------------------------|
| DOCKER   | Yes       | Supported for Docker    |
| LOCAL    | No        | Not supported for local |
| CAO      | No        | Not supported for cao   |
| CLOUD    | No        | Not supported for cloud |

## Chaos kill-sockets command

Cbdinocluster provides a way to kill (shutdown, terminate, close) couchbase
service sockets running on a docker container by using `chaos kill-sockets`
command.

This method is used to simulate an unexpected severance of the connection by
the third party between the server and the SDK.

### What it does

If no `nodeId` is provided, the command closes the connections to all nodes in
the cluster. If a `nodeId` is specified, only the connections for that node will
be terminated. The command first gathers information about the nodes, including
the containers they are running in, and then builds a filter for the `ss(8)`
command from the [iproute2][iproute2-repo] project using the selected Couchbase
services (by default, only KV ports are closed).

If the node is running in privileged mode (the cluster definition has the
`docker.privileged` flag set to `true`), the command will attempt to install
iproute2 inside the container and terminate the connections locally. Otherwise,
it attempts to use `ss` on the Docker host. If neither the container is running
in privileged mode nor the host has `iproute2` installed, the command will fail
with an error.

### Usage

This command is useful when the network link between the SDK and the Couchbase
server is unstable but still allows a connection to be established. Unlike the
chaos `*-traffic` commands, this method does not use firewall rules to block
traffic. Instead, it only affects connections that are already established at
the time of command execution. In general, both the SDK and the server are
resilient to this type of issue, but the command may still uncover hidden
problems related to resource reuse and operation retries.

### Kill Couchbase Service

Terminate KV connections for all nodes of the cluster

```
cbdinocluster chaos kill-sockets <clusterId>
```

Terminate Query and KV connections for the chosen node in the cluster

```
cbdinocluster chaos kill-sockets <clusterId> [nodeId] --service n1ql --service kv
```

Sample config that runs containers in privileged mode:

```yaml
nodes:
  - count: 4
    version: 7.6.3-4200
    services:
      - kv
      - n1ql
      - index
docker:
  kv-memory: 2000
  cbas-memory: 3000
  fts-memory: 3000
  privileged: true
```


### Supported Deployers

| Deployer | Supported | Notes                   |
|----------|-----------|-------------------------|
| DOCKER   | Yes       | Supported for Docker    |
| LOCAL    | No        | Not supported for local |
| CAO      | No        | Not supported for cao   |
| CLOUD    | No        | Not supported for cloud |

[iproute2-repo]: https://git.kernel.org/pub/scm/network/iproute2/iproute2.git
