<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type TrafficSnapshot } from '$lib/api'
  import { formatBytes, formatCount, formatTime } from '$lib/format'
  import { startPolling } from '$lib/poll'
  import * as Alert from '$lib/components/ui/alert'
  import * as Card from '$lib/components/ui/card'
  import * as Table from '$lib/components/ui/table'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { CircleAlert, Inbox, Pause, Play, Search } from 'lucide-svelte'

  let { onUnauthorized }: { onUnauthorized: () => void } = $props()

  type SortMode = 'bytes' | 'recent' | 'conns'
  const sortModes: { value: SortMode; label: string }[] = [
    { value: 'bytes', label: '流量' },
    { value: 'recent', label: '最近' },
    { value: 'conns', label: '连接数' },
  ]

  let traffic = $state<TrafficSnapshot | null>(null)
  let error = $state('')
  let paused = $state(false)
  let filter = $state('')
  let sort: SortMode = $state('bytes')

  const activeSortLabel = $derived(sortModes.find((m) => m.value === sort)?.label ?? '流量')

  const visibleDomains = $derived.by(() => {
    const domains = traffic?.domains ?? []
    const q = filter.trim().toLowerCase()
    if (!q) return domains
    return domains.filter((d) => d.domain.toLowerCase().includes(q))
  })

  async function refresh() {
    if (paused) return
    try {
      traffic = await api.traffic(sort)
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

  const headCell = 'sticky top-0 z-10 bg-card'
</script>

{#if error}
  <Alert.Alert variant="destructive" class="mb-4">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>{error}</Alert.AlertDescription>
  </Alert.Alert>
{:else if !traffic}
  <Loading />
{:else if (traffic.domains ?? []).length === 0}
  <div class="flex flex-col items-center gap-2 py-10 text-sm text-muted-foreground">
    <Inbox class="size-5" aria-hidden="true" />
    <p>暂无流量记录。</p>
  </div>
{:else}
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
          onclick={() => {
            sort = m.value
            void refresh()
          }}
        >
          {m.label}
        </button>
      {/each}
    </div>
    <Button
      variant="outline"
      size="sm"
      class="ml-auto shrink-0 gap-1.5"
      aria-pressed={paused}
      onclick={() => (paused = !paused)}
    >
      {#if paused}
        <Play class="size-3.5" />
        继续
      {:else}
        <Pause class="size-3.5" />
        暂停
      {/if}
    </Button>
    <Badge variant="secondary" class="shrink-0">
      {#if visibleDomains.length === (traffic.domains ?? []).length}
        {formatCount(visibleDomains.length)} domains
      {:else}
        {formatCount(visibleDomains.length)} / {formatCount((traffic.domains ?? []).length)} domains
      {/if}
    </Badge>
  </div>
  <Card.Card class="max-h-[70vh] overflow-auto">
    <Table.Table>
      <Table.TableHeader>
        <Table.TableRow>
          <Table.TableHead class={headCell}>Domain</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Requests</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Up</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Down</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Total</Table.TableHead>
          <Table.TableHead class={headCell + ' text-right'}>Last seen</Table.TableHead>
        </Table.TableRow>
      </Table.TableHeader>
      <Table.TableBody>
        {#if visibleDomains.length === 0}
          <Table.TableRow>
            <Table.TableCell colspan={6} class="py-8 text-center text-sm text-muted-foreground">
              没有匹配“{filter}”的域名。
            </Table.TableCell>
          </Table.TableRow>
        {/if}
        {#each visibleDomains as d (d.domain)}
          <Table.TableRow>
            <Table.TableCell class="max-w-[20rem] truncate font-mono text-xs">{d.domain}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatCount(d.conns)}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatBytes(d.bytesUp)}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatBytes(d.bytesDown)}</Table.TableCell>
            <Table.TableCell class="text-right font-medium tabular-nums">
              {formatBytes(d.bytesUp + d.bytesDown)}
            </Table.TableCell>
            <Table.TableCell class="text-right text-muted-foreground tabular-nums">
              {formatTime(d.lastSeen)}
            </Table.TableCell>
          </Table.TableRow>
        {/each}
      </Table.TableBody>
    </Table.Table>
  </Card.Card>
  <p class="mt-2 text-xs text-muted-foreground">
    按{activeSortLabel}排序的前 100 个域名{paused ? '，自动刷新已暂停。' : '，每 5 秒刷新。'}
  </p>
{/if}
