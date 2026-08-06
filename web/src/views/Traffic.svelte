<script lang="ts">
  import { type Source } from '$lib/api'
  import { formatBytes, formatCount, formatTime } from '$lib/format'
  import { connectLive, live } from '$lib/live.svelte.ts'
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

  // The domain table is fed by the shared SSE stream, filtered by the
  // connection params below.
  const traffic = $derived(live.traffic)
  let filter = $state('')
  let sort: SortMode = $state('bytes')
  let client = $state('')

  const activeSortLabel = $derived(sortModes.find((m) => m.value === sort)?.label ?? '流量')
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
    const domains = traffic?.domains ?? []
    const q = filter.trim().toLowerCase()
    if (!q) return domains
    return domains.filter((d) => d.domain.toLowerCase().includes(q))
  })

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

{#if !traffic}
  <Loading />
{:else}
  {#if !live.connected}
    <p class="mb-3 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive">
      实时连接已断开，正在自动重连…
    </p>
  {/if}
  <div class="mb-3 flex items-center gap-2">
    <div class="relative min-w-0 flex-1 sm:max-w-xs">
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
    <Badge variant="secondary" class="ml-auto shrink-0">
      {#if visibleDomains.length === (traffic.domains ?? []).length}
        {formatCount(visibleDomains.length)} domains
      {:else}
        {formatCount(visibleDomains.length)} / {formatCount((traffic.domains ?? []).length)} domains
      {/if}
    </Badge>
  </div>
  {#if (traffic.clients ?? []).length > 0}
    <div class="mb-3 flex flex-wrap items-center gap-1.5" role="group" aria-label="客户端过滤">
      <button
        type="button"
        class="rounded-full border px-3 py-1 text-xs font-medium transition-colors {!client
          ? 'border-primary/40 bg-primary/10 text-foreground'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={!client}
        onclick={() => (client = '')}
      >
        全部客户端
      </button>
      {#each traffic.clients as c}
        <button
          type="button"
          class="rounded-full border px-3 py-1 font-mono text-xs transition-colors {client === c.ip
            ? 'border-primary/40 bg-primary/10 text-foreground'
            : 'text-muted-foreground hover:text-foreground'}"
          aria-pressed={client === c.ip}
          title={`${formatCount(c.conns)} 次 · ↑${formatBytes(c.bytesUp)} ↓${formatBytes(c.bytesDown)}`}
          onclick={() => (client = c.ip)}
        >
          {c.ip}
          <span class="ms-1 text-muted-foreground">{formatCount(c.conns)}</span>
        </button>
      {/each}
    </div>
  {/if}
  {#if (traffic.domains ?? []).length === 0}
    <div class="flex flex-col items-center gap-2 rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
      <Inbox class="size-5" aria-hidden="true" />
      <p>暂无{sourceLabel}记录。</p>
    </div>
  {:else}
    <Card.Card class="max-h-[70vh] overflow-auto">
    <Table.Table>
      <Table.TableHeader>
        <Table.TableRow>
          <Table.TableHead class={headCell}>Domain</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Requests</Table.TableHead>
          {#if !isDNSView}
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
            <Table.TableCell colspan={isDNSView ? 4 : 7} class="py-8 text-center text-sm text-muted-foreground">
              没有匹配“{filter}”的域名。
            </Table.TableCell>
          </Table.TableRow>
        {/if}
        {#each visibleDomains as d (d.domain)}
          <Table.TableRow>
            <Table.TableCell class="max-w-[20rem] truncate font-mono text-xs">{d.domain}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatCount(d.conns)}</Table.TableCell>
            {#if !isDNSView}
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
  <p class="mt-2 text-xs text-muted-foreground">
    按{activeSortLabel}排序的前 100 个{isDNSView ? 'DNS 查询域名' : sourceLabel + '域名'}，实时推送。
  </p>
{/if}
