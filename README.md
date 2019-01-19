# sower
[![GitHub release](http://img.shields.io/github/release/wweir/sower.svg?style=popout)](https://github.com/wweir/sower/releases)
[![CircleCI](https://img.shields.io/circleci/token/64114421aab286b6e918741071535250a5273f06/project/github/wweir/sower/master.svg?style=popout)](https://circleci.com/gh/wweir/sower)
[![GitHub issue](https://img.shields.io/github/issues/wweir/sower.svg?style=popout)](https://github.com/wweir/sower/issues)
[![GitHub star](https://img.shields.io/github/stars/wweir/sower.svg?style=popout)](https://github.com/wweir/sower/stargazers)
[![GitHub license](https://img.shields.io/github/license/wweir/sower.svg?style=popout)](LICENSE)

The sower is a cross-platform transparent proxy tool base on DNS solution.

If you wanna enjoy the sower, you need to deploy sower in both server and client side.

In client side, sower listening UDP `53` and TCP `80`/`443` ports, so that you need run it with privileged.

In server side, sower needs no privileged. It just listening to a port (default `5533`), parse and relay the request to target server.

Sower also provides an http(s) proxy, which listens to port `8080` by default. You can turn it off or use another port at any time.

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

## Installation
### Auto deploy
Auto deploy script support Linux server side and masOS/Linux client side.

```
$ bash -c "$(curl -s https://raw.githubusercontent.com/wweir/sower/master/deploy/install)"
```

Then modify the configuration file as needed and set `127.0.0.1` as your first domain name server.

If you wanna uninstall sower, change `install` into `uninstall` and rerun the command.

### Manually deploy
1. Download the precompiled file from https://github.com/wweir/sower/releases
2. Decompression the file into a folder
3. Run `./sower -h` for help
5. Config domain name server
4. Config auto start

## todo
- [x] authenticate
- [ ] broker
- [x] CI/CD
- [ ] relay optimization
- [ ] deploy script for all normal platform