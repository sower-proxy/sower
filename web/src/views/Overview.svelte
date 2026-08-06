<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type Status, type TrafficSnapshot } from '$lib/api'
  import { formatBytes, formatUptime, formatCount } from '$lib/format'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import { CircleAlert } from 'lucide-svelte'

  let { onUnauthorized }: { onUnauthorized: () => void } = $props()

  let status = $state<Status | null>(null)
  let traffic = $state<TrafficSnapshot | null>(null)
  let error = $state('')

  async function refresh() {
    try {
      const [s, t] = await Promise.all([api.status(), api.traffic()])
      status = s
      traffic = t
      error = ''
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'refresh failed'
    }
  }

  onMount(() => {
    refresh()
    const timer = setInterval(refresh, 5000)
    return () => clearInterval(timer)
  })
</script>

{#if error}
  <Alert.Alert variant="destructive">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>{error}</Alert.AlertDescription>
  </Alert.Alert>
{:else if !status || !traffic}
  <p class="py-8 text-center text-sm text-muted-foreground">Loading…</p>
{:else}
  <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>版本</Card.CardDescription>
        <Card.CardTitle class="text-xl">{status.version || '-'}</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <Badge variant="secondary">{status.date || 'unknown build date'}</Badge>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>运行时长</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatUptime(traffic.uptime)}</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">自启动以来</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>DNS 查询</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(traffic.dnsQueries)}</Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">全部上游查询</p>
      </Card.CardContent>
    </Card.Card>

    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>代理流量</Card.CardDescription>
        <Card.CardTitle class="text-xl">
          {formatBytes(traffic.bytesUp + traffic.bytesDown)}
        </Card.CardTitle>
      </Card.CardHeader>
      <Card.CardContent>
        <p class="text-xs text-muted-foreground">
          ↑ {formatBytes(traffic.bytesUp)} / ↓ {formatBytes(traffic.bytesDown)}
        </p>
      </Card.CardContent>
    </Card.Card>
  </div>

  <div class="mt-4 grid gap-4 sm:grid-cols-3">
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>HTTP 连接</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(traffic.conns.http)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>HTTPS 连接</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(traffic.conns.https)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>SOCKS5 连接</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(traffic.conns.socks5)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
  </div>

  <div class="mt-4 grid gap-4 sm:grid-cols-3">
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>Block 规则</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(status.rules.block)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>Direct 规则</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(status.rules.direct)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
    <Card.Card>
      <Card.CardHeader>
        <Card.CardDescription>Proxy 规则</Card.CardDescription>
        <Card.CardTitle class="text-xl">{formatCount(status.rules.proxy)}</Card.CardTitle>
      </Card.CardHeader>
    </Card.Card>
  </div>
{/if}
