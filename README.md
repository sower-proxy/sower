# sower
[![GitHub release](http://img.shields.io/github/release/wweir/sower.svg?style=popout)](https://github.com/wweir/sower/releases)
[![CircleCI](https://img.shields.io/circleci/token/64114421aab286b6e918741071535250a5273f06/project/github/wweir/sower/master.svg?style=popout)](https://circleci.com/gh/wweir/sower)
[![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/wweir/sower.svg?style=popout)](https://hub.docker.com/r/wweir/sower)
[![GitHub issue](https://img.shields.io/github/issues/wweir/sower.svg?style=popout)](https://github.com/wweir/sower/issues)
[![GitHub star](https://img.shields.io/github/stars/wweir/sower.svg?style=popout)](https://github.com/wweir/sower/stargazers)
[![GitHub license](https://img.shields.io/github/license/wweir/sower.svg?style=popout)](LICENSE)

中文介绍见 [Wiki](https://github.com/wweir/sower/wiki)

The sower is a cross-platform intelligent transparent proxy tool base on DNS solution.

The first time you visit a new website, sower will detect if the domain in block list and add it in suggect list. So that, you do not need to care about the rules, sower will handle it in a intelligent way.

If you wanna enjoy the sower, you need to deploy sower on both server and client side.
On client side, sower listening UDP `53` and TCP `80`/`443` ports, so that you need run it with privileged.
On server side, it just listening to a port (default `5533`), parse and relay the request to target server.

Sower also provides an http(s) proxy listening on `:8080` by default. You can turn it off or use another port at any time.


## Installation
After Deployed, please check your config file, it is placed in `/usr/local/etc/sower.toml` by default. Here is the example config file [**conf/sower.toml**](https://github.com/wweir/sower/blob/master/conf/sower.toml)

### Auto deploy
Auto deploy script support Linux server side and masOS/Linux client side.

```shell
$ bash -c "$(curl -sL https://git.io/fhhdp)"
```

Then modify the configuration file as needed and set `127.0.0.1` as your first domain name server.
In most situation, you just need to modify `/etc/resolv.conf`.

If you wanna uninstall sower, run:

```shell
$ bash -c "$(curl -sL https://git.io/fhjer)"
```

### Manually deploy
1. Download the precompiled file from https://github.com/wweir/sower/releases
2. Decompression the file into a folder
3. Run `./sower -h` for help
5. Config domain name server
4. Config auto start

### Docker deploy
The auto build docker images are [wweir/sower](https://hub.docker.com/r/wweir/sower).

It is very simple to use it on the server side. Export the port(5533) and run it directly.

But the client is more troublesome and needs some understanding of the working mechanism of the sower.


## Architecture
```
          request target servers
<-------------+              +------------->
              |              |
              |              |
      +------------server-------------+
      |       | relay service|        |
      | +-----+---------------------+ |
      | |                           | |
      | | parse http(s) target url  | |
      | |                           | |
      | +---------------------------+ |
      |     shadow service            |
      +--------^----------------------+
               |           request domain server
       quic / KCP / TCP         +---------->
               |                |
      +--------+---client+------+-----+
      |                  |            |
      |  shadow service  |            |
      |  relay service   |     dns    |
      |                  |   service  |
      |                  |            |
      |       127.0.0.1 or other      |
      |                  |            |
      +-^-----^----------+---^----^---+
        |     |              |    |
        |     |              |    |   +----->
http(s) proxy |   +----------+    |   |
              2   1               1   2
              +   +               +   +
         blocked request      normal request

```
For more detail, see [透明代理 Sower 技术剖析](https://wweir.cc/post/%E9%80%8F%E6%98%8E%E4%BB%A3%E7%90%86-sower-%E6%8A%80%E6%9C%AF%E5%89%96%E6%9E%90/)


## Todo
- [x] authenticate
- [ ] broker(waiting for QUIC implementation to be stable)
- [x] CI/CD
- [x] relay optimization
- [x] deploy script for all normal platform
- [x] dns rule intelligent suggestions
- [x] use socks5 as upstream proxy
- [ ] multi port http_proxy support