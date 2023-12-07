# cbdinocluster

cbdinocluster is the successor to cbdyncluster. It's intention is to provide tooling
for the dynamic allocation of clusters on developer machines as well in various CI
environments.

## Getting Started

### Dependancies

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

- Download the latest release
  ```
  https://github.com/couchbaselabs/cbdinocluster/releases
  ```
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


### Advanced Usage

#### Resetting Colima

In the case that your colima docker instance becomes corrupted, or stops working
as expected, you can destroy it using `colima delete`. Once your colima instance
has been destroyed, you can start it again using the same command from the
_Dependancies_ steps above, followed by running `cbdinocluster init` again.
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

#### x86_64 Images

Prior to Couchbase Server 7.1, our docker containers were not built for
arm64. On a typical colima instance, these do not run properly due to
the massive performance impact of emulating amd64. Using the method
mentioned in the _High Performance Virtualization_, we enable Apple's
Rosetta virtualization which allows these instances to execute at nearly
native speed. Note that due to a bug in Apple's hypervisor framework,
some Couchbase Server images using old kernels will panic and fail to
start, this is fixed in Mac OS X 13.5+.
