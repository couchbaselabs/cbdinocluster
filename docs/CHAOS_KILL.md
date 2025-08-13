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
