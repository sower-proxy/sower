<script lang="ts">
  import { api, ApiError, type Category, type DomainTest } from '$lib/api'
  import { formatCount } from '$lib/format'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Textarea from '$lib/components/ui/textarea/textarea.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { CircleAlert, ListX, Search } from 'lucide-svelte'

  let { category, onUnauthorized }: { category: Category; onUnauthorized: () => void } = $props()

  const pageSize = 200

  let rules = $state<string[] | null>(null)
  let total = $state(0)
  let offset = $state(0)
  let draft = $state('')
  let query = $state('')
  let error = $state('')
  let busy = $state(false)
  let loadingMore = $state(false)
  let requestID = 0
  let searchTimer: ReturnType<typeof setTimeout> | undefined

  function requestIsCurrent(id: number, requestedCategory: Category) {
    return id === requestID && requestedCategory === category
  }

  async function load(reset: boolean) {
    const id = ++requestID
    const requestedCategory = category
    const requestedQuery = query
    if (!reset) loadingMore = true
    try {
      const resp = await api.rules(requestedCategory, {
        q: requestedQuery,
        offset: reset ? 0 : offset,
        limit: pageSize,
      })
      if (!requestIsCurrent(id, requestedCategory)) return
      rules = reset ? resp.rules : [...(rules ?? []), ...resp.rules]
      total = resp.total
      offset = resp.offset + resp.rules.length
      error = ''
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'load failed'
    } finally {
      if (requestIsCurrent(id, requestedCategory)) loadingMore = false
    }
  }

  // Reload the currently loaded range in place after add/remove, so the
  // scroll position survives while the totals stay accurate.
  async function reloadView() {
    const id = ++requestID
    const requestedCategory = category
    const loadedCount = Math.max(rules?.length ?? pageSize, pageSize)
    try {
      const resp = await api.rules(requestedCategory, { q: query, offset: 0, limit: loadedCount })
      if (!requestIsCurrent(id, requestedCategory)) return
      rules = resp.rules
      total = resp.total
      offset = resp.offset + resp.rules.length
      error = ''
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'refresh failed'
    }
  }

  async function addRules() {
    const items = draft
      .split('\n')
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
    if (items.length === 0 || busy) return

    const id = ++requestID
    const requestedCategory = category
    busy = true
    error = ''
    try {
      await api.addRules(requestedCategory, items)
      if (!requestIsCurrent(id, requestedCategory)) return
      draft = ''
      await reloadView()
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'add failed'
    } finally {
      if (requestIsCurrent(id, requestedCategory)) {
        busy = false
      }
    }
  }

  async function removeRule(rule: string) {
    if (busy) return

    const id = ++requestID
    const requestedCategory = category
    busy = true
    error = ''
    try {
      await api.removeRules(requestedCategory, [rule])
      if (!requestIsCurrent(id, requestedCategory)) return
      await reloadView()
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'remove failed'
    } finally {
      if (requestIsCurrent(id, requestedCategory)) {
        busy = false
      }
    }
  }

  // Domain routing test: report which rules match a domain and the route a
  // connection to it would take, without live detection.
  let testDomain = $state('')
  let testResult = $state<DomainTest | null>(null)
  let testing = $state(false)
  let testError = $state('')

  const routeLabels: Record<DomainTest['route'], string> = {
    block: '拦截',
    direct: '直连',
    proxy: '代理',
    auto: '自动检测',
  }

  type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline'

  function routeVariant(route: DomainTest['route']): BadgeVariant {
    switch (route) {
      case 'block':
        return 'destructive'
      case 'direct':
        return 'secondary'
      case 'proxy':
        return 'default'
      default:
        return 'outline'
    }
  }

  function matchVariant(category: Category, matched: boolean): BadgeVariant {
    if (!matched) return 'outline'
    switch (category) {
      case 'block':
        return 'destructive'
      case 'direct':
        return 'secondary'
      default:
        return 'default'
    }
  }

  async function runTest() {
    const domain = testDomain.trim()
    if (!domain || testing) return
    testing = true
    testError = ''
    testResult = null
    try {
      testResult = await api.rulesTest(domain)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      testError = e instanceof Error ? e.message : 'test failed'
    } finally {
      testing = false
    }
  }

  // Reset and reload whenever the breadcrumb-driven category changes.
  $effect(() => {
    category
    clearTimeout(searchTimer)
    rules = null
    total = 0
    offset = 0
    query = ''
    error = ''
    busy = false
    void load(true)
  })

  function onSearchInput(event: Event) {
    query = (event.currentTarget as HTMLInputElement).value
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      offset = 0
      void load(true)
    }, 300)
  }
</script>

{#if error}
  <Alert.Alert variant="destructive" class="mb-4">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>{error}</Alert.AlertDescription>
  </Alert.Alert>
{/if}

<Card.Card class="mb-4">
  <Card.CardContent class="grid gap-3 pt-6">
    <div class="flex items-center gap-2">
      <Input
        bind:value={testDomain}
        placeholder="检测域名路由，如 example.com"
        aria-label="检测域名"
        onkeydown={(e) => {
          if (e.key === 'Enter') void runTest()
        }}
      />
      <Button onclick={runTest} disabled={testing || !testDomain.trim()}>
        {testing ? '检测中…' : '检测'}
      </Button>
    </div>
    {#if testError}
      <p class="text-sm text-destructive">{testError}</p>
    {/if}
    {#if testResult}
      <div class="grid gap-2">
        <div class="flex items-center gap-2 text-sm">
          <span class="text-muted-foreground">路由</span>
          <Badge variant={routeVariant(testResult.route)}>{routeLabels[testResult.route]}</Badge>
          <code class="min-w-0 break-all font-mono">{testResult.domain}</code>
        </div>
        <div class="grid gap-1.5">
          {#each testResult.matches as m}
            <div class="flex items-center gap-2 text-sm">
              <Badge variant={matchVariant(m.category, m.matched)}>
                {m.category}{m.matched ? '' : ' 未命中'}
              </Badge>
              {#if m.matched}
                <code class="min-w-0 break-all font-mono text-xs text-muted-foreground">{m.rule}</code>
              {/if}
            </div>
          {/each}
        </div>
        {#if testResult.note}
          <p class="text-xs text-muted-foreground">{testResult.note}</p>
        {/if}
      </div>
    {/if}
  </Card.CardContent>
</Card.Card>

{#if rules === null}
  <Loading />
{:else if total === 0}
  <div class="flex flex-col items-center gap-2 py-10 text-sm text-muted-foreground">
    <ListX class="size-5" aria-hidden="true" />
    <p>{query.trim() ? `没有匹配“${query.trim()}”的规则。` : '该分类暂无规则。'}</p>
  </div>
{:else}
  <div class="mb-3 flex items-center gap-2">
    <div class="relative min-w-0 flex-1 sm:max-w-xs">
      <Search class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={query}
        oninput={onSearchInput}
        placeholder="搜索规则…"
        aria-label="搜索规则"
        class="pl-8"
      />
    </div>
    <Badge variant="secondary" class="ml-auto shrink-0">
      {#if rules.length === total}
        {formatCount(total)} rules
      {:else}
        {formatCount(rules.length)} / {formatCount(total)} rules
      {/if}
    </Badge>
  </div>
  <Card.Card>
    <Card.CardContent class="divide-y p-0">
      {#each rules as rule (rule)}
        <div class="flex items-center justify-between gap-3 px-4 py-2.5 transition-colors hover:bg-muted/40">
          <code class="min-w-0 break-all font-mono text-sm">{rule}</code>
          <Button
            variant="ghost"
            size="sm"
            class="shrink-0 text-destructive hover:bg-destructive/10 hover:text-destructive"
            onclick={() => removeRule(rule)}
            disabled={busy}
          >
            Remove
          </Button>
        </div>
      {/each}
    </Card.CardContent>
  </Card.Card>
  {#if rules.length < total}
    <Button
      variant="outline"
      class="mt-3 w-full"
      onclick={() => load(false)}
      disabled={loadingMore || busy}
    >
      {loadingMore
        ? '加载中…'
        : `加载更多（已加载 ${formatCount(rules.length)} / ${formatCount(total)}）`}
    </Button>
  {/if}
{/if}
