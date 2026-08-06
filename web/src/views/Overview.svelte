<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type Status, type TrafficSnapshot } from '$lib/api'
  import { formatBytes, formatUptime, formatCount } from '$lib/format'
  import { startPolling } from '$lib/poll'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import {
    ArrowDownUp,
    Ban,
    CircleAlert,
    Clock3,
    Globe,
    Link,
    Lock,
    Network,
    Package,
    Route,
    Zap,
  } from 'lucide-svelte'

  let { status, onUnauthorized }: { status: Status | null; onUnauthorized: () => void } = $props()

  let traffic = $state<TrafficSnapshot | null>(null)
  let error = $state('')

  async function refresh() {
    try {
      traffic = await api.traffic()
      error = ''
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'refresh failed'
    }
  }

  onMount(() => startPolling(refresh, 5000))
</script>

{#if error}
  <Alert.Alert variant="destructive">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>{error}</Alert.AlertDescription>
  </Alert.Alert>
{:else if !status || !traffic}
  <Loading />
{:else}
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

  <div class="mt-4 grid gap-4 sm:grid-cols-3">
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>HTTP 连接</Card.CardDescription>
          <Link class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(traffic.conns.http)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>HTTPS 连接</Card.CardDescription>
          <Lock class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(traffic.conns.https)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>SOCKS5 连接</Card.CardDescription>
          <Network class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(traffic.conns.socks5)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
  </div>

  <div class="mt-4 grid gap-4 sm:grid-cols-3">
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>Block 规则</Card.CardDescription>
          <Ban class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(status.rules.block)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>Direct 规则</Card.CardDescription>
          <Zap class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(status.rules.direct)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <div class="flex items-center justify-between gap-2">
          <Card.CardDescription>Proxy 规则</Card.CardDescription>
          <Route class="size-4 text-muted-foreground" />
        </div>
        <Card.CardTitle class="text-xl tabular-nums">{formatCount(status.rules.proxy)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
  </div>
{/if}
