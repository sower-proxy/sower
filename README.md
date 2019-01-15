# sower
Yet another cross platform transparent proxy tool

## architecture
```
          request target servers
<-------------+              +------------->
              |              |
              |              |
      +------------server-------------+
      |       | relay service|        |
      | +-----+---------------------+ |
      | |                           | |
      | | parsr http(s) target url  | |
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
      |                  |    server  |
      |                  |            |
      |       127.0.0.1 or other      |
      |                  |            |
      +-------^----------+---^----^---+
              |              |    |
              |              |    |   +----->
              |   +----------+    |   |
              2   1               1   2
              +   +               +   +
         blocked request      normal request

```

## install
1. install server on `server node` by `make server`
2. write config file, example: [conf/sower.toml](https://github.com/wweir/sower/blob/master/conf/sower.toml)
3. install client on `client node` by `make client`
4. add `127.0.0.1` as you first domain server

## todo
- [x] authenticate
- [ ] broker