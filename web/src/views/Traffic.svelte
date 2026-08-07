<script lang="ts">
  import { type Source } from '$lib/api'
  import { formatBytes, formatCount, formatTime } from '$lib/format'
  import { rateSeries, ts } from '$lib/history'
  import { connectLive, live } from '$lib/live.svelte.ts'
  import AreaChart from '$lib/components/AreaChart.svelte'
  import * as Card from '$lib/components/ui/card'
  import * as Table from '$lib/components/ui/table'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { Inbox, Search } from 'lucide-svelte'

  let { source = 'all' }: { source?: Source } = $props()

  type SortMode = 'bytes' | 'recent' | 'conns'
  const sortModes: { value: SortMode; label: string }[] = [
    { value: 'bytes', label: '流量' },
    { value: 'recent', label: '最近' },
    { value: 'conns', label: '连接数' },
  ]

  type View = 'live' | 'totals' | 'blocked'
  let view = $state<View>('live')

  // Live view is fed by the SSE traffic events; totals are the cumulative
  // since-start view pushed every 30s.
  const domains = $derived(
    view === 'totals' ? (live.totals?.domains ?? []) : (live.traffic?.domains ?? []),
  )
  const clients = $derived(
    view === 'totals' ? (live.totals?.clients ?? []) : (live.traffic?.clients ?? []),
  )
  let filter = $state('')
  let sort: SortMode = $state('bytes')
  let client = $state('')

  // Blocked domains are global: block decisions carry no source or client
  // dimension, so the list ignores the page's traffic filters.
  const blocked = $derived(live.traffic?.blocked ?? [])
  const visibleBlocked = $derived.by(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return blocked
    return blocked.filter((b) => b.domain.toLowerCase().includes(q))
  })

  const activeSortLabel = $derived(sortModes.find((m) => m.value === sort)?.label ?? '流量')

  // Chart band: the history series is process-global, not affected by the
  // table's source/client filters — the labels say so explicitly.
  const history = $derived(live.history)
  const chartLabels = $derived(history.map((h) => ts(h.at)))
  const chartTimestamps = $derived(history.map((h) => h.at))
  const rates = $derived(rateSeries(history, ['bytesUp', 'bytesDown', 'conns', 'dns']))
  const fmtRate = (v: number) => `${formatBytes(v)}/s`
  const fmtPerSec = (v: number) => `${v.toFixed(1)}/s`
  const sourceLabels: Record<Source, string> = {
    all: '流量',
    http: 'HTTP',
    https: 'HTTPS',
    socks5: 'SOCKS5',
    dns: 'DNS',
  }
  const sourceLabel = $derived(sourceLabels[source])
  const isDNSView = $derived(source === 'dns')

  const visibleDomains = $derived.by(() => {
    const q = filter.trim().toLowerCase()
    if (!q) return domains
    return domains.filter((d) => d.domain.toLowerCase().includes(q))
  })

  // The totals view spans every source, so byte columns stay visible even
  // when the breadcrumb has narrowed the live view to DNS.
  const showBytes = $derived(view === 'totals' || !isDNSView)

  // Re-point the SSE stream whenever a page filter changes.
  $effect(() => {
    const params = new URLSearchParams()
    if (sort !== 'bytes') params.set('sort', sort)
    if (source !== 'all') params.set('source', source)
    if (client) params.set('client', client)
    connectLive(params.toString())
  })

  const headCell = 'sticky top-0 z-10 bg-card'
</script>

{#if !live.traffic && !live.totals}
  <Loading />
{:else}
  {#if !live.connected}
    <p class="mb-3 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive">
      实时连接已断开，正在自动重连…
    </p>
  {/if}
  <div class="mb-3 grid gap-2 sm:flex sm:items-center">
    <div class="relative min-w-0 w-full sm:w-64">
      <Search class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        bind:value={filter}
        placeholder="过滤域名…"
        aria-label="过滤域名"
        class="pl-8"
      />
    </div>
    <div
      class="flex shrink-0 items-center gap-0.5 rounded-md border bg-muted/50 p-0.5"
      role="group"
      aria-label="数据范围"
    >
      <button
        type="button"
        class="rounded-sm px-2.5 py-1 text-xs font-medium transition-colors {view === 'live'
          ? 'bg-card text-foreground shadow-sm'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={view === 'live'}
        onclick={() => (view = 'live')}
      >
        实时
      </button>
      <button
        type="button"
        class="rounded-sm px-2.5 py-1 text-xs font-medium transition-colors {view === 'totals'
          ? 'bg-card text-foreground shadow-sm'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={view === 'totals'}
        onclick={() => (view = 'totals')}
      >
        总计
      </button>
      <button
        type="button"
        class="rounded-sm px-2.5 py-1 text-xs font-medium transition-colors {view === 'blocked'
          ? 'bg-card text-foreground shadow-sm'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={view === 'blocked'}
        onclick={() => (view = 'blocked')}
      >
        拦截
      </button>
    </div>
    {#if view === 'live'}
      <div
        class="flex shrink-0 items-center gap-0.5 rounded-md border bg-muted/50 p-0.5"
        role="group"
        aria-label="排序方式"
      >
        {#each sortModes as m}
          <button
            type="button"
            class="rounded-sm px-2.5 py-1 text-xs font-medium transition-colors {sort === m.value
              ? 'bg-card text-foreground shadow-sm'
              : 'text-muted-foreground hover:text-foreground'}"
            aria-pressed={sort === m.value}
            onclick={() => (sort = m.value)}
          >
            {m.label}
          </button>
        {/each}
      </div>
    {/if}
    <Badge variant="secondary" class="justify-self-end shrink-0 sm:ml-auto">
      {#if view === 'blocked'}
        {formatCount(visibleBlocked.length)} blocked
      {:else if visibleDomains.length === domains.length}
        {formatCount(visibleDomains.length)} domains
      {:else}
        {formatCount(visibleDomains.length)} / {formatCount(domains.length)} domains
      {/if}
    </Badge>
  </div>
  {#if view !== 'blocked' && clients.length > 0}
    <div class="mb-3 flex flex-wrap items-center gap-1.5" role="group" aria-label="客户端过滤">
      <button
        type="button"
        class="rounded-full border px-3 py-1 text-xs font-medium transition-colors {view === 'live' && !client
          ? 'border-primary/40 bg-primary/10 text-foreground'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={view === 'live' && !client}
        onclick={() => (client = '')}
      >
        全部客户端
      </button>
      {#each clients as c}
        <button
          type="button"
          class="rounded-full border px-3 py-1 font-mono text-xs transition-colors {view === 'live' && client === c.ip
            ? 'border-primary/40 bg-primary/10 text-foreground'
            : 'text-muted-foreground hover:text-foreground'}"
          aria-pressed={view === 'live' && client === c.ip}
          title={`${formatCount(c.conns)} 次 · ↑${formatBytes(c.bytesUp)} ↓${formatBytes(c.bytesDown)}`}
          onclick={() => {
            if (view === 'totals') view = 'live'
            client = c.ip
          }}
        >
          {c.ip}
          <span class="ms-1 text-muted-foreground">{formatCount(c.conns)}</span>
        </button>
      {/each}
    </div>
  {/if}
  <div class="mb-4 grid gap-4 sm:grid-cols-2">
    <div>
      <p class="mb-1 text-xs text-muted-foreground">上下行速率 · 全局历史</p>
      <AreaChart
        series={[
          { name: '下行', color: 'var(--chart-1)', values: rates.bytesDown },
          { name: '上行', color: 'var(--chart-4)', values: rates.bytesUp },
        ]}
        labels={chartLabels}
        timestamps={chartTimestamps}
        format={fmtRate}
        height={120}
      />
    </div>
    <div>
      <p class="mb-1 text-xs text-muted-foreground">连接与查询 · 全局历史</p>
      <AreaChart
        series={[
          { name: '连接', color: 'var(--chart-3)', values: rates.conns },
          { name: 'DNS', color: 'var(--chart-2)', values: rates.dns },
        ]}
        labels={chartLabels}
        timestamps={chartTimestamps}
        format={fmtPerSec}
        height={120}
      />
    </div>
  </div>
  {#if view === 'blocked'}
    {#if blocked.length === 0}
      <div class="flex flex-col items-center gap-2 rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
        <Inbox class="size-5" aria-hidden="true" />
        <p>暂无拦截记录。</p>
      </div>
    {:else}
      <Card.Card class="max-h-[70vh] overflow-auto">
      <Table.Table>
        <Table.TableHeader>
          <Table.TableRow>
            <Table.TableHead class={headCell}>Domain</Table.TableHead>
            <Table.TableHead class={headCell + ' text-right'}>Blocked</Table.TableHead>
            <Table.TableHead class={headCell + ' text-right'}>Last seen</Table.TableHead>
          </Table.TableRow>
        </Table.TableHeader>
        <Table.TableBody>
          {#if visibleBlocked.length === 0}
            <Table.TableRow>
              <Table.TableCell colspan={3} class="py-8 text-center text-sm text-muted-foreground">
                没有匹配“{filter}”的域名。
              </Table.TableCell>
            </Table.TableRow>
          {/if}
          {#each visibleBlocked as b (b.domain)}
            <Table.TableRow>
              <Table.TableCell class="max-w-[20rem] truncate font-mono text-xs">{b.domain}</Table.TableCell>
              <Table.TableCell class="text-right tabular-nums">{formatCount(b.count)}</Table.TableCell>
              <Table.TableCell class="text-right text-muted-foreground tabular-nums">
                {formatTime(b.lastSeen)}
              </Table.TableCell>
            </Table.TableRow>
          {/each}
        </Table.TableBody>
      </Table.Table>
      </Card.Card>
    {/if}
  {:else}
  {#if domains.length === 0}
    <div class="flex flex-col items-center gap-2 rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
      <Inbox class="size-5" aria-hidden="true" />
      <p>{view === 'totals' ? '暂无总计记录。' : `暂无${sourceLabel}记录。`}</p>
    </div>
  {:else}
    <Card.Card class="max-h-[70vh] overflow-auto">
    <Table.Table>
      <Table.TableHeader>
        <Table.TableRow>
          <Table.TableHead class={headCell}>Domain</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Requests</Table.TableHead>
          {#if showBytes}
            <Table.TableHead class={headCell + ' text-right'}>Up</Table.TableHead>
            <Table.TableHead class={headCell + ' text-right'}>Down</Table.TableHead>
            <Table.TableHead class={headCell + ' text-right'}>Total</Table.TableHead>
          {/if}
          <Table.TableHead class={headCell}>Client</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Last seen</Table.TableHead>
        </Table.TableRow>
      </Table.TableHeader>
      <Table.TableBody>
        {#if visibleDomains.length === 0}
          <Table.TableRow>
            <Table.TableCell colspan={showBytes ? 7 : 4} class="py-8 text-center text-sm text-muted-foreground">
              没有匹配“{filter}”的域名。
            </Table.TableCell>
          </Table.TableRow>
        {/if}
        {#each visibleDomains as d (d.domain)}
          <Table.TableRow>
            <Table.TableCell class="max-w-[20rem] truncate font-mono text-xs">{d.domain}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatCount(d.conns)}</Table.TableCell>
            {#if showBytes}
              <Table.TableCell class="text-right tabular-nums">{formatBytes(d.bytesUp)}</Table.TableCell>
              <Table.TableCell class="text-right tabular-nums">{formatBytes(d.bytesDown)}</Table.TableCell>
              <Table.TableCell class="text-right font-medium tabular-nums">
                {formatBytes(d.bytesUp + d.bytesDown)}
              </Table.TableCell>
            {/if}
            <Table.TableCell class="max-w-[9rem] truncate font-mono text-xs text-muted-foreground" title={d.lastClientIP}>
              {d.lastClientIP || '—'}
            </Table.TableCell>
            <Table.TableCell class="text-right text-muted-foreground tabular-nums">
              {formatTime(d.lastSeen)}
            </Table.TableCell>
          </Table.TableRow>
        {/each}
      </Table.TableBody>
    </Table.Table>
    </Card.Card>
  {/if}
  {/if}
  {#if view === 'blocked'}
    <p class="mt-2 text-xs text-muted-foreground">
      自启动以来被拦截的域名，按拦截次数排序，实时推送。
    </p>
  {:else if view === 'totals'}
    <p class="mt-2 text-xs text-muted-foreground">
      自启动以来按流量排序的前 {formatCount(domains.length)} 个域名与客户端，每 30 秒更新。
    </p>
  {:else}
    <p class="mt-2 text-xs text-muted-foreground">
      按{activeSortLabel}排序的前 100 个{isDNSView ? 'DNS 查询域名' : sourceLabel + '域名'}，实时推送。
    </p>
  {/if}
{/if}
