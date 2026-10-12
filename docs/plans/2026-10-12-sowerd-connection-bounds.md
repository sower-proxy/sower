# Plan: sowerd 连接资源上界（2026-10-12 OOM 复盘）

状态：已定稿并回写（2026-10-12）。本文为非权威历史记录，权威规则见 [security.md](../security.md) 的
"Connection Resource Bounds"。

实施结果摘要：

- 落地 commit `28c4f9e` / `3c869cd` / `06c357c`：`newRedirectServer` 补齐 :80 的四个超时；
  `serve443` 加 `maxInboundConns = 2048` 并发上界；`writeStallListener`/`writeStallConn` 位于
  TLS 之下，为每次写入武装 `writeStallTimeout`；`dumpHeapOnSignal`（`heapdump_unix.go`，
  其他平台 no-op）在 SIGUSR1 时写 heap profile；`GOMEMLIMIT=512MiB` 进入安装器 unit、
  打包 unit 与 kr drop-in。
- 发布：`v1.7.2` 的 Release run 在 windows-amd64/arm64 上失败（`syscall.SIGUSR1` 在
  Windows 未定义），Attach release assets 被跳过、没有发布任何资产；`06c357c` 把信号
  处理挪到 `//go:build unix` 文件后以 `v1.7.3` 重新发布（12 个平台构建 + Attach assets 全绿）。
- kr 运行的是 **v1.7.3 release 资产**：tarball `sower-linux-amd64.tar.gz` 经
  `SHA256SUMS` 校验，`sowerd` sha256 `74b34fd9119d45d0d8de2013656a292ed2ffa08ec7e6a2dff6cac9262fd6d428`，
  `go version -m` 显示 `mod v1.7.3`、`vcs.revision=06c357c`、`vcs.modified=false`；
  备份 `/usr/local/bin/sowerd.bak-20261012191818`（上一版为本地构建 v1.7.1-4-g28c4f9e，
  备份 `sowerd.bak-20261012175623`）。
- 验证：`:80` 仍 308 跳转；`:443` 回退反代 200；SIGUSR1 产出
  `/var/cache/sower/sowerd-heap-*.pprof`；用 `transport/sower` 从本机经 kr 中继
  `www.baidu.com:80` 得到 HTTP/1.0 200（证明新增的写超时不误伤中继）；r6c 客户端
  socks5 到 google 仍 204，其日志自 17:31 起无握手失败；`GOMEMLIMIT=512MiB` 在进程中可见。

## 事件

2026-10-12 17:30:39，kr（1.6 GiB 内存、2 vCPU、zram swap `vm.swappiness=0`）上的
sowerd 被内核 OOM killer 杀掉：`anon-rss:969556kB`、`total-vm:2742656kB`。同一时刻
内核任务表里没有第二个大内存进程（其余全部 < 15 MB）；kill 之后 sar 显示空闲内存
从 ~100 MB 回到 1.1 GB、page cache 从 1.03 GB 掉到 168 MB，即被释放的那 1 GB 就是
sowerd。

## 证据

- 被杀的是 v1.7.1（`/usr/local/bin/sowerd`，go1.26.8），16:50:20 启动，17:30:39 被杀。
  启动 10 分钟内 journald 已报 `Under memory pressure, flushing caches`（17:00:45）。
- sowerd 自身日志在 16:58:48 之后静默 32 分钟。成功路径只在 debug 级别记录，
  `:80` 路径完全不记录，因此"无日志"不等于"无活动"，只能说明这些连接走的是
  静默路径。
- kr 的 sar 缺了 17:00–17:20 的采样（机器被压住），17:30:46 那条是 16:50→17:30 的
  均值：`%system 60.4`、`%iowait 16.3`、`ldavg 87`，网卡 rx 63.8 KB/s、tx 70.7 KB/s
  （双向近似对称）。
- 同一二进制在 16:00–16:50 的实例峰值只有 27.4 MB；更早的实例（go1.27.x）跑 8 h /
  39 h 峰值 24.7 / 28.8 MB。所以这**不是**随时间稳定累积的泄漏曲线，更像某个窗口
  内的突发累积（`total-vm` 2.68 GB ≈ 启动基线 1.33 GB 的两倍）。
- r6c 的 sower 客户端从 17:02:35 起所有到 kr:443 的 utls 握手都 10 s 超时
  （`utls handshake: read tcp ...->47.80.11.226:443: i/o timeout`），直到 sowerd
  17:31:45 重启后立刻恢复。即 sowerd 在 16:58–17:30 对合法客户端已不可服务。
- 内核侧没有 accept 队列溢出（`TcpExtListenOverflows`、`TCPBacklogDrop`、
  `TCPReqQFullDrop` 全为 0），说明连接是被应用层接收后才留下的，不是内核丢包。

## 分析

sowerd 自身没有按连接或按流量增长的全局结构：没有按主机名的 map，autocert 的
`state` 被 3 个白名单域名封顶，basic-auth 失败表上限 4096 且未启用。因此这 1 GB
常驻只能是"被保活的连接（goroutine + 缓冲 + socket）"或"运行时堆的一次突发峰值
未归还"。代码里有三处**上界缺失**，任一处都能让一次连接突发变成常驻内存：

1. `:80` 的 `http.Server` 完全没有超时：连接只要不把请求发完，就永久持有
   goroutine、socket 与缓冲；而这是对整个互联网开放的端口。
2. `:443` 的 accept 循环没有并发上界：每个连接在握手阶段最多被持有 60 s
   （`tlsHandshakeTimeout`），突发速率直接决定常驻内存；握手失败是静默返回。
3. `relay.Relay`（conns v0.1.1）只设置读超时：一端停止读取时两个方向都会阻塞，
   `wg.Wait()` 永不返回，连接对、fd 与 goroutine 永久泄漏；也因为函数不返回，
   连 `relay conn` 日志都不会出现。

另有两处放大因素：进程与 unit 都没有内存上限（对比 sower 客户端早就有
`SOWER_MEMORY_LIMIT_MB`，commit d0031e4），所以 sowerd 长到 1 GB 时死的是整台机器；
kr 的 zram swap 因 `vm.swappiness=0` 从未被使用。

## 处置

1. `:80` 监听器补齐 `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/`IdleTimeout`
   （`newRedirectServer`），让半发、空转、keep-alive 的连接不能长期占用。
2. `:443` accept 循环加并发上界 `maxInboundConns = 2048`，超限直接关闭连接（不入队）。
3. 增加本地诊断入口：收到 SIGUSR1 时把 heap profile 写入缓存目录
   （`systemctl kill -s USR1 sowerd`，`go tool pprof -http :0 <file>`），不新增网络面。
4. 部署侧：sowerd 单元加 `Environment=GOMEMLIMIT=512MiB`，让失控堆退化服务而不是
   杀死宿主机。
5. 发布工具链：v1.7.1 是唯一用 go1.26.8 构建的制品（现场其余二进制为
   go1.27.1/1.27.2），而 go1.26 的 Green Tea GC 有公开的内存回归报告
   （golang/go#77567、#77361）。CI 的 `go-version: ^1.26` 收敛到 `^1.27`。

## 未决

- **驱动已由另一会话定位（2026-10-12 18:44–18:50，board 消息 `m-77507977-64f`）**：任何
  指名 `sower.wweir.fun` 的代理请求会形成自持链——该名字的公网 A 记录指向 r6c 的
  tailscale 地址 `100.64.1.1`（r6c 上 sower 监听 80/443/53/1080），`cmd/sower` 无条件下
  发给 kr，kr 解析同名后回dial 进 r6c:80，再转发，逐跳生成下一跳。实测 r6c 上
  `100.64.1.1:80` 连接数 155→254→305→495→601（对端全是 kr），且触发它的 curl 退出后
  仍在增长；kr 上 sowerd 同期 311 个 dial、649 个 fd。这与本次 OOM 的窗口、双向近似
  对称流量、静默增长、以及“sowerd 一死链就断”全部吻合；18:50 重启 r6c 的 sower 后两侧
  都回到 0 连接。sowerd 侧的 cap 与写超时只把它**限住**（链在 2048 处停止增长、内存有界），
  真正的修法是客户端拒绝把请求 dial 回自己的监听地址（另一会话已开高优先级 finding）。
  触发链是否就是 17:30 那次膨胀的唯一成因仍无直接证据（当时进程已死、无 pprof、sar 缺样），
  因此上界与 SIGUSR1 dump 的价值是把下一次复现变成可定位事件，而不是宣称已修复根因。
- `relay.Relay` 的写超时缺陷属于 `sower-proxy/conns` 仓库，应在那里修（finding
  `f-1ae16668-b92`）。sowerd 侧只做上界，不给隧道加全局空闲超时，避免误杀合法但长时间
  空闲的隧道。
- 客户端侧另有一处同类症状需要单独排查：r6c 的 sower 客户端
  （v1.6.2 / go1.27.2）`VmRSS 1.36 GB`、`VmData 1.6 GB`，但 fd 只有 36 个。这与
  "连接被保活"（fd 驱动）不是同一个根因，更像按请求/流量累积的堆数据
  （`cmd/sower` 的 `stats.BindConn`/`WrapConn` 与 admin 统计路径）；r6c 的 procd
  也没有设置 `SOWER_MEMORY_LIMIT_MB`。sowerd 的改动不会消除它。
