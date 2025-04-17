# cbdinocluster experimental features

## External Docker Host

While the use of an external docker host removes some of the benefits of the
architecture that cbdinocluster is based on, it is sometimes a "neccessary
evil" in supporting some deployment strategies. Specifically, due to the
inability for Windows WSL to support any form of multi-ip networking with
docker, one of the ways to support deployment of clusters from a Windows
system is to operate docker on an external system which is able to grant IP
addresses which are addressible from the parent network (your home network).

The following describes the steps I took to do this on my own home network:

#### Adjust Home Network

I adjusted my router's home subnet from being 192.168.0.1/24 to
192.168.0.1/23 instead. This provides an additional subnet range of
192.168.1.1 -> 192.168.1.255 to utilize from my docker host. It is
important to ensure that you only expand the subnet range of the network
_without_ expanding the IP range used by the router DHCP server. The
additional IP addresses will be managed by docker rather than DHCP and
must never conflict with the routers automatic assignments.

#### Install Docker

I then installed docker on a Ubuntu server in my network which is given
an IP address by my routers DHCP server (192.168.0.157 in my case).

#### Expose Docker via TCP

I updated the docker service file to expose a TCP socket for controlling
docker using the following changes:

Edit the service file:

```
sudo systemctl edit docker.service
```

Service File Edit (note we must clear ExecStart first, then overwrite it):

```
[Service]
ExecStart=
ExecStart=/usr/bin/dockerd -H fd:// -H tcp://0.0.0.0:2375
```

Reload Docker:

```
sudo systemctl daemon-reload
sudo systemctl restart docker.service
```

#### Setup Docker Network

I then configured a docker ipvlan network which sets up docker to create
containers in the IP range of my home network:

```
docker network create -d ipvlan \
    --subnet=192.168.0.0/23 \
    --gateway=192.168.0.1 \
    --ip-range=192.168.1.96/28 \
    -o ipvlan_mode=l2 \
    -o parent=eth0 homenet
```

#### Configure DinoCluster

I was then able to configure cbdinocluster on my windows system to leverage
this custom-created docker network like so:

```
-- Docker Configuration
Would you like to configure Docker Deployments? [Y/n]:
What docker host should we use? []: tcp://192.168.0.157:2375
Pinging the docker host to confirm it works...
Success!
Listing docker networks:
  bridge
  homenet
  host
  none
This does not appear to be colima, cannot auto-create dinonet network.
What docker network should we use? []: homenet
```
