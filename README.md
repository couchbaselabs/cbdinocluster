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

### Advanced Usage

#### Resetting Colima

In the case that your colima docker instance becomes corrupted, or stops working
as expected, you can destroy it using `colima delete`. Once your colima instance
has been destroyed, you can start it again using the same command from the
_Dependancies_ steps above, followed by running `cbdinocluster init` again.
Reinitializing dinocluster will maintain your existing configuration, but will
apply the neccessary colima configurations that were lost during the recreation.

#### x86_64 Images

Prior to Couchbase Server 7.1, our docker containers were not built for
arm64, making it impossible to run them inside a arm64 colima instance.
You can add the `--arch x86_64` option to `colima start` in this case to
force colima to run in a virtualized `x86_64` environment. This option will
incur a performance penalty due to virtualization, but will enable the execution
of these older version. If you've previously run `colima start`, it will be
neccessary to follow the _Resetting Colima_ steps above to change these options.
