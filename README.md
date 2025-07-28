# cbdinocluster

cbdinocluster is the successor to cbdyncluster. Its intention is to provide tooling
for the dynamic allocation of clusters on developer machines as well as in various CI
environments.

## Getting Started

### Dependencies

#### Mac OS (M1 and x86_64)

- Docker CLI (not neccessarily Docker for Desktop)
  ```
  brew install docker
  ```
- Colima w/ Network Address

  ```
  brew install colima
  colima start --network-address --cpu 4 --memory 6
  # you can later use `colima stop` to stop it
  # or `colima delete` to completely destroy it
  ```

#### Linux

- Docker

#### Windows

- Not yet supported, but possible with cbdynvagrant

### Installing

- Download the latest [release](https://github.com/couchbaselabs/cbdinocluster/releases)

- [Generate personal access token (classic)](https://github.com/settings/tokens) with `read:packages` to access
non-public server builds on the GitHub Package Registry.
See [Managing your personal access tokens](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens).

- Setup cbdinocluster
  ```
  ./cbdinocluster init
  ```
  Note that on MacOS, you may need to remove the quarantine attribute with a command like this:
  ```
  sudo xattr -r -d com.apple.quarantine $PWD/cbdinocluster
  ```

### Using cbdinocluster

#### List your local clusters

```
./cbdinocluster ps
```

#### Allocate a simple local 3-node cluster

```
./cbdinocluster allocate simple:7.2.0
```

#### Allocate a single-node cluster with higher memory allocations

Useful for testing magma buckets, advanced search indexes (1536mb for KV, 1024mb for Indexer, 1024mb for FTS)

```
./cbdinocluster allocate high-mem:7.2.0
```

#### Remove a previously allocated local cluster

```
./cbdinocluster rm {{CLUSTER_ID}}
```

#### Create a bucket named `default`

```
./cbdinocluster buckets add {{CLUSTER_ID}} default --ram-quota-mb=100 --flush-enabled=true
```

#### Create a collection in the default scope on the bucket named `default`

```
./cbdinocluster collections add {{CLUSTER_ID}} default _default test
```

#### Load travel sample bucket

```
./cbdinocluster buckets load-sample {{CLUSTER_ID}} travel-sample
```

#### Use JSON output to get connection string of the first cluster

```
cbdinocluster connstr $(cbdinocluster ps --json | jq -r '.[0].id')
```

### Advanced Usage

#### Resetting Colima

In the case that your colima docker instance becomes corrupted, or stops working
as expected, you can destroy it using `colima delete`. Once your colima instance
has been destroyed, you can start it again using the same command from the
_Dependencies_ steps above, followed by running `cbdinocluster init` again.
Reinitializing dinocluster will maintain your existing configuration, but will
apply the neccessary colima configurations that were lost during the recreation.

#### High Performance Virtualization

Mac OS X 13+ supports a built in virtualization hypervisor which significantly
improves performance compared to the typical QEMU emulation. This can be enabled
using the options described below to your `colima start` command. If you've
previously run `colima start`, it will be neccessary to follow the
_Resetting Colima_ steps above to change these options.

```
colima start --network-address --cpu 4 --memory 6 --arch aarch64 --vm-type=vz --vz-rosetta
```

#### Kubernetes Support

cbdinocluster has support for spinning up Couchbase Clusters within kubernetes
using the Couchbase Autonomous Operator. During initialization, it is possible
to configure the k8s cluster you wish to use. Additionally, Colima has built
in support for Kubernetes using the `--kubernetes` flag. If you've previously
run `colima start`, it will be neccessary to follow the _Resetting Colima_
steps above to change this option.

For instance, on an M1 Mac you might do:

```
colima start --network-address --kubernetes --cpu 4 --memory 6 --arch aarch64 --vm-type=vz --vz-rosetta
```

Or running typically you might use:

```
colima start --network-address --kubernetes --cpu 4 --memory 6
```

#### x86_64 Images

Prior to Couchbase Server 7.1, our docker containers were not built for
arm64. On a typical colima instance, these do not run properly due to
the massive performance impact of emulating amd64. Using the method
mentioned in the _High Performance Virtualization_, we enable Apple's
Rosetta virtualization which allows these instances to execute at nearly
native speed. Note that due to a bug in Apple's hypervisor framework,
some Couchbase Server images using old kernels will panic and fail to
start, this is fixed in Mac OS X 13.5+.

### Additional References

This section contains useful references that can help when trying to solve
problems in actual use of cbdinocluster.

#### Using prefixes to match cluster and container IDs

To avoid constantly copying and pasting IDs in terminal, it is recommended to use only unique prefix.

```
~ $ cbdinocluster ps
...
Clusters:
  4f9e6625-6f48-4866-bab9-076ae5f57052 [State: ready, Timeout: none, Deployer: docker]
    a20294bf-44fc-4a6f-b019-a005d2fbf16b                       192.168.107.130      af3dcbd5...
    86da5182-9f3c-450e-8b69-1c65fc646f7b                       192.168.107.128      b6b7ab97...
    51024e57-660d-497d-8878-42ca7781ab4c                       192.168.107.129      2ca9e448...
```

Note that the we have only one cluster, and its ID starts with `4`, so it is enough to refer to the cluster with
cbdinocluster commands.

```
~ $ cbdinocluster connstr 4
...
couchbase://192.168.107.130,192.168.107.128,192.168.107.129
```

The same work with docker itself (first node ID starts with `a`):

```
~ $ docker exec -ti a /usr/bin/bash
root@af3dcbd5b2a4:/# ps aux | grep memcached
couchba+     384  0.0  0.4 1762336 27268 ?       SLsl 20:39   0:00 /opt/couchbase/bin/memcached -C /opt/couchbase/var/lib/couchbase/config/memcached.json
root         664  0.0  0.0   2976  1536 pts/0    S+   20:49   0:00 grep --color=auto memcached
```

#### Bash function to render connection string with cluster certificate

This version uses option name for C++SDK (and all wrappers)

```bash
cbdinocluster_connstr_with_certificate() {
    local cluster_id="$1"
    local cert_file="${TMPDIR}/cluster-${cluster_id}.pem"
    cbdinocluster certificates getca "${cluster_id}" > "${cert_file}"
    echo "$(cbdinocluster connstr --tls "${cluster_id}")?trust_certificate=${cert_file}"
}
```
