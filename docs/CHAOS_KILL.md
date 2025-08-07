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

### Kill Couchbase Service

```
cbdinocluster chaos kill-couchbase <clusterId> [nodeId]
```
