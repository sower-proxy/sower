<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type TrafficSnapshot } from '$lib/api'
  import { formatBytes, formatCount, formatTime } from '$lib/format'
  import * as Alert from '$lib/components/ui/alert'
  import * as Card from '$lib/components/ui/card'
  import * as Table from '$lib/components/ui/table'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import { CircleAlert } from 'lucide-svelte'

  let { onUnauthorized }: { onUnauthorized: () => void } = $props()

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

  onMount(() => {
    refresh()
    const timer = setInterval(refresh, 5000)
    return () => clearInterval(timer)
  })
</script>

{#if error}
  <Alert.Alert variant="destructive" class="mb-4">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>{error}</Alert.AlertDescription>
  </Alert.Alert>
{:else if !traffic}
  <p class="py-8 text-center text-sm text-muted-foreground">Loading…</p>
{:else if (traffic.domains ?? []).length === 0}
  <p class="py-8 text-center text-sm text-muted-foreground">No traffic recorded yet.</p>
{:else}
  <Card.Card>
    <Table.Table>
      <Table.TableHeader>
        <Table.TableRow>
          <Table.TableHead>Domain</Table.TableHead>
          <Table.TableHead class="text-right">Requests</Table.TableHead>
          <Table.TableHead class="text-right">Up</Table.TableHead>
          <Table.TableHead class="text-right">Down</Table.TableHead>
          <Table.TableHead class="text-right">Total</Table.TableHead>
          <Table.TableHead class="text-right">Last seen</Table.TableHead>
        </Table.TableRow>
      </Table.TableHeader>
      <Table.TableBody>
        {#each traffic.domains ?? [] as d (d.domain)}
          <Table.TableRow>
            <Table.TableCell class="max-w-[20rem] truncate font-mono text-xs">{d.domain}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatCount(d.conns)}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatBytes(d.bytesUp)}</Table.TableCell>
            <Table.TableCell class="text-right tabular-nums">{formatBytes(d.bytesDown)}</Table.TableCell>
            <Table.TableCell class="text-right font-medium tabular-nums">
              {formatBytes(d.bytesUp + d.bytesDown)}
            </Table.TableCell>
            <Table.TableCell class="text-right text-muted-foreground">
              {formatTime(d.lastSeen)}
            </Table.TableCell>
          </Table.TableRow>
        {/each}
      </Table.TableBody>
    </Table.Table>
  </Card.Card>
  <div class="mt-2 flex items-center justify-between">
    <p class="text-xs text-muted-foreground">按流量排序的前 100 个域名，每 5 秒刷新。</p>
    <Badge variant="outline">{formatCount(traffic.domains.length)} domains</Badge>
  </div>
{/if}
