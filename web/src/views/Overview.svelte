<script lang="ts">
  import { type Status } from '$lib/api'
  import { formatBytes, formatCount, formatTime, formatUptime } from '$lib/format'
  import { rateSeries, ts } from '$lib/history'
  import { live } from '$lib/live.svelte.ts'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import AreaChart from '$lib/components/AreaChart.svelte'
  import {
    ArrowDown,
    ArrowDownUp,
    ArrowUp,
    CircleAlert,
    Clock3,
    Gauge,
    Globe,
    Link,
    Network,
    Package,
    Route,
  } from 'lucide-svelte'

  let { status }: { status: Status | null } = $props()

  // Traffic and history arrive via the shared SSE stream.
  const traffic = $derived(live.traffic)
  const history = $derived(live.history)

  // Proxy-side failures (upstream dial, DNS, accept) surface as a service
  // alert; blocked connections are routing decisions and never counted. The
  // alert reflects the current state: it only shows while a failure happened
  // within alertWindowMs, so a past outage does not keep a red banner up
  // forever. The cumulative counts are shown as context inside it.
  const alertWindowMs = 10 * 60 * 1000
  const errorTotal = $derived(
    traffic ? traffic.errors.dial + traffic.errors.dns + traffic.errors.accept : 0,
  )
  const errorActive = $derived.by(() => {
    const first = traffic?.events[0]
    return !!first && Date.now() - new Date(first.at).getTime() < alertWindowMs
  })
  const recentErrors = $derived(traffic?.events.slice(0, 3) ?? [])
  const errorKindLabel = (kind: string) =>
    kind === 'dial' ? '连接上游失败' : kind === 'dns' ? 'DNS 解析失败' : '连接接收失败'

  // --- history series helpers ---
  const labels = $derived(history.map((h) => ts(h.at)))
  const timestamps = $derived(history.map((h) => h.at))
  const rates = $derived(
    rateSeries(history, ['bytesUp', 'bytesDown', 'dns', 'conns', 'proxy', 'direct', 'block']),
  )

  const fmtRate = (v: number) => `${formatBytes(v)}/s`
  const fmtPerSec = (v: number) => `${v.toFixed(1)}/s`

  const rangeNote = $derived.by(() => {
    const first = history[0]
    const last = history[history.length - 1]
    return first && last
      ? `${ts(first.at)} – ${ts(last.at)} · 进程内历史，重启后清空`
      : '进程内历史，重启后清空'
  })
</script>

{#if !live.connected}
  <Alert.Alert variant="destructive" class="mb-4">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>实时连接不可用，正在自动重连…</Alert.AlertDescription>
  </Alert.Alert>
{:else if live.stale}
  <Alert.Alert variant="destructive" class="mb-4">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>连接正常，但数据已停止更新，请刷新页面</Alert.AlertDescription>
  </Alert.Alert>
{/if}
{#if !status || !traffic}
  <Loading />
{:else}
  {#if errorActive}
    <Alert.Alert variant="destructive" class="mb-4">
      <CircleAlert class="size-4" />
      <Alert.AlertDescription>
        <p>
          服务异常 {formatCount(errorTotal)} 次
          <span class="tabular-nums">
            （上游连接 {formatCount(traffic.errors.dial)} · DNS
            {formatCount(traffic.errors.dns)} · 接收 {formatCount(traffic.errors.accept)}）
          </span>
        </p>
        {#if recentErrors.length > 0}
          <ul class="mt-2 space-y-1 text-xs">
            {#each recentErrors as ev, i}
              <li class="tabular-nums">
                <span class="text-muted-foreground">{formatTime(ev.at)}</span>
                <span class="text-destructive">{errorKindLabel(ev.kind)}</span>
                <span class="break-all">{ev.detail}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </Alert.AlertDescription>
    </Alert.Alert>
  {/if}
  <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>版本</Card.CardDescription>
          <Package class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl">{status.version || '-'}</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <Badge variant="secondary">{status.date || 'unknown build date'}</Badge>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>运行时长</Card.CardDescription>
          <Clock3 class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatUptime(traffic.uptime)}</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">自启动以来</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>DNS 查询</Card.CardDescription>
          <Globe class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(traffic.dnsQueries)}</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">全部上游查询</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>代理流量</Card.CardDescription>
          <ArrowDownUp class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">
          {formatBytes(traffic.bytesUp + traffic.bytesDown)}
        </Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground tabular-nums">
          ↑ {formatBytes(traffic.bytesUp)} / ↓ {formatBytes(traffic.bytesDown)}
        </p>
      </Card.CardContent>
    </Card.Card>
  </div>

  <div class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>上行速率</Card.CardDescription>
          <ArrowUp class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatBytes(traffic.rates.bytesUpPerSec)}/s</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">近 60 秒均值</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>下行速率</Card.CardDescription>
          <ArrowDown class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatBytes(traffic.rates.bytesDownPerSec)}/s</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">近 60 秒均值</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>DNS 速率</Card.CardDescription>
          <Globe class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{traffic.rates.dnsPerSec.toFixed(1)}/s</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">近 60 秒均值</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>连接速率</Card.CardDescription>
          <Link class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{traffic.rates.connsPerSec.toFixed(1)}/s</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">近 60 秒均值</p>
      </Card.CardContent>
    </Card.Card>
  </div>

  <div class="mt-4 grid gap-4 sm:grid-cols-3">
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>活跃连接</Card.CardDescription>
          <Network class="size-4 text-muted-foreground" />
        </div>
      </Card.CardHeader>
      <Card.CardContent class="space-y-2">
        <div class="flex items-center justify-between text-sm">
          <span class="inline-flex items-center gap-1.5 text-muted-foreground">
            <span class="size-2 rounded-full" style="background:var(--chart-1)"></span>HTTP
          </span>
          <span class="tabular-nums">{traffic.active.http}</span>
        </div>
        <div class="flex items-center justify-between text-sm">
          <span class="inline-flex items-center gap-1.5 text-muted-foreground">
            <span class="size-2 rounded-full" style="background:var(--chart-4)"></span>HTTPS
          </span>
          <span class="tabular-nums">{traffic.active.https}</span>
        </div>
        <div class="flex items-center justify-between text-sm">
          <span class="inline-flex items-center gap-1.5 text-muted-foreground">
            <span class="size-2 rounded-full" style="background:var(--chart-3)"></span>SOCKS5
          </span>
          <span class="tabular-nums">{traffic.active.socks5}</span>
        </div>
        <p class="border-t pt-2 text-xs text-muted-foreground tabular-nums">
          累计 {formatCount(traffic.conns.http)} / {formatCount(traffic.conns.https)} / {formatCount(traffic.conns.socks5)}
        </p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>规则命中</Card.CardDescription>
          <Route class="size-4 text-muted-foreground" />
        </div>
      </Card.CardHeader>
      <Card.CardContent class="space-y-2">
        <div class="flex items-center justify-between text-sm">
          <span class="inline-flex items-center gap-1.5 text-muted-foreground">
            <span class="size-2 rounded-full" style="background:var(--chart-5)"></span>Block
          </span>
          <span class="tabular-nums">{formatCount(traffic.ruleHits.block)}</span>
        </div>
        <div class="flex items-center justify-between text-sm">
          <span class="inline-flex items-center gap-1.5 text-muted-foreground">
            <span class="size-2 rounded-full" style="background:var(--chart-2)"></span>Direct
          </span>
          <span class="tabular-nums">{formatCount(traffic.ruleHits.direct)}</span>
        </div>
        <div class="flex items-center justify-between text-sm">
          <span class="inline-flex items-center gap-1.5 text-muted-foreground">
            <span class="size-2 rounded-full" style="background:var(--chart-1)"></span>Proxy
          </span>
          <span class="tabular-nums">{formatCount(traffic.ruleHits.proxy)}</span>
        </div>
        <p class="border-t pt-2 text-xs text-muted-foreground tabular-nums">
          已配置 {formatCount(status.rules.block)} / {formatCount(status.rules.direct)} / {formatCount(status.rules.proxy)} 条
        </p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>系统</Card.CardDescription>
          <Gauge class="size-4 text-muted-foreground" />
        </div>
      </Card.CardHeader>
      <Card.CardContent class="space-y-2">
        <div class="flex items-center justify-between text-sm">
          <span class="text-muted-foreground">Goroutines</span>
          <span class="tabular-nums">{formatCount(traffic.system.goroutines)}</span>
        </div>
        <div class="flex items-center justify-between text-sm">
          <span class="text-muted-foreground">堆内存</span>
          <span class="tabular-nums">{formatBytes(traffic.system.heapAlloc)}</span>
        </div>
      </Card.CardContent>
    </Card.Card>
  </div>

  <div class="mt-4 grid gap-4 lg:grid-cols-2">
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>流量历史</Card.CardDescription>
        <Card.CardTitle class="text-base">上下行速率</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <AreaChart
          label="上下行速率历史"
          series={[
            { name: '下行', color: 'var(--chart-1)', values: rates.bytesDown },
            { name: '上行', color: 'var(--chart-4)', values: rates.bytesUp },
          ]}
          labels={labels}
          {timestamps}
          format={fmtRate}
        />
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>DNS 与连接</Card.CardDescription>
        <Card.CardTitle class="text-base">每秒查询 / 新建连接</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <AreaChart
          label="DNS 与连接速率历史"
          series={[
            { name: 'DNS', color: 'var(--chart-2)', values: rates.dns },
            { name: '连接', color: 'var(--chart-3)', values: rates.conns },
          ]}
          labels={labels}
          {timestamps}
          format={fmtPerSec}
        />
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>活跃连接</Card.CardDescription>
        <Card.CardTitle class="text-base">分协议在线数</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <AreaChart
          label="活跃连接历史"
          series={[
            { name: 'HTTP', color: 'var(--chart-1)', values: history.map((h) => h.activeHttp) },
            { name: 'HTTPS', color: 'var(--chart-4)', values: history.map((h) => h.activeHttps) },
            { name: 'SOCKS5', color: 'var(--chart-3)', values: history.map((h) => h.activeSocks) },
          ]}
          labels={labels}
          {timestamps}
          format={formatCount}
        />
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>规则命中</Card.CardDescription>
        <Card.CardTitle class="text-base">每秒路由决策</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <AreaChart
          label="规则命中速率历史"
          series={[
            { name: 'Proxy', color: 'var(--chart-1)', values: rates.proxy },
            { name: 'Direct', color: 'var(--chart-2)', values: rates.direct },
            { name: 'Block', color: 'var(--chart-5)', values: rates.block },
          ]}
          labels={labels}
          {timestamps}
          format={fmtPerSec}
        />
      </Card.CardContent>
    </Card.Card>
  </div>

  <p class="mt-3 text-xs text-muted-foreground">{rangeNote}</p>
{/if}
