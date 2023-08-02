# cbdinocluster

cbdinocluster is the successor to cbdyncluster.  It's intention is to provide tooling
for the dynamic allocation of clusters on developer machines as well in various CI
environments.

## Getting Started

### Dependancies

#### Mac OS (M1 and x86_64)

* Docker CLI (not neccessarily Docker for Desktop)
  ```
  brew install docker
  ```
* Colima w/ Network Address
  ```
  brew install colima
  colima start --network-address --cpu 4 --memory 6
  # you can later use `colima stop` to stop it
  ```

#### Linux

* Docker

#### Windows

* Not yet supported, but possible with cbdynvagrant

### Installing

* Download the latest release
    ```
    https://github.com/couchbaselabs/cbdinocluster/releases
    ```
* Setup cbdinocluster
    ```
    ./cbdinocluster init
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
