<script lang="ts">
  import { fade } from 'svelte/transition'
  import { untrack } from 'svelte'
  import { prefersReducedMotion } from 'svelte/motion'
  import { api, ApiError, type Category, type DomainTest, type RuleChangeSet, type RuleEntry, type RuleHit, type RuleSort, type RuleSortDir } from '$lib/api'
  import { formatCount, formatTime } from '$lib/format'
  import * as Card from '$lib/components/ui/card'
  import * as Table from '$lib/components/ui/table'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { ArrowDown, ArrowUp, Check, ChevronsUpDown, CircleAlert, Inbox, ListX, Plus, RotateCcw, Search, Trash2, Undo2 } from 'lucide-svelte'

  let { category, onUnauthorized }: { category: Category | 'miss'; onUnauthorized: () => void } = $props()

  const pageSize = 200

  let rules = $state<RuleEntry[] | null>(null)
  let total = $state(0)
  let offset = $state(0)
  let newRule = $state('')
  let query = $state('')
  // Hit stats are tracked per category (block/direct/proxy rule hits since
  // start); every category shows the stat columns and can sort by them.
  let sortBy: RuleSort = $state('default')
  let sortDir: RuleSortDir = $state('desc')
  let error = $state('')
  let busy = $state(false)
  let loadingMore = $state(false)
  let searching = $state(false)
  // Focus target for the rule list: after a delete the row disappears and
  // focus would fall back to <body>; parking it on the list keeps the
  // reading position stable for keyboard users.
  let listRef: HTMLElement | null = $state(null)
  let requestID = 0
  let searchTimer: ReturnType<typeof setTimeout> | undefined
  // Rule-less connection stats: domains that matched no block/direct/proxy
  // rule, aggregated per domain with connection count and last access.
  let missSort: 'count' | 'recent' = $state('count')
  let missDomains = $state<RuleHit[] | null>(null)
  let missError = $state('')

  function requestIsCurrent(id: number, requestedCategory: Category) {
    return id === requestID && requestedCategory === category
  }

  async function load(reset: boolean) {
    // Rule operations only run for real categories; the miss view is global.
    if (category === 'miss') return
    const id = ++requestID
    const requestedCategory = category
    const requestedQuery = query
    if (!reset) loadingMore = true
    try {
      const resp = await api.rules(requestedCategory, {
        q: requestedQuery,
        offset: reset ? 0 : offset,
        limit: pageSize,
        sort: sortBy,
        dir: sortDir,
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
    if (category === 'miss') return
    const id = ++requestID
    const requestedCategory = category
    const loadedCount = Math.max(rules?.length ?? pageSize, pageSize)
    try {
      const resp = await api.rules(requestedCategory, { q: query, offset: 0, limit: loadedCount, sort: sortBy, dir: sortDir })
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

  async function addRule() {
    if (category === 'miss') return
    const rule = newRule.trim()
    if (!rule || busy) return

    const id = ++requestID
    const requestedCategory = category
    busy = true
    error = ''
    try {
      await api.addRules(requestedCategory, [rule])
      if (!requestIsCurrent(id, requestedCategory)) return
      newRule = ''
      // Feedback first: the reload below can be slow on huge rule sets and
      // must not delay or swallow the success signal.
      flashSaved('已添加规则 ', rule)
      await reloadView()
      if (category !== requestedCategory) return
      await refreshChanges()
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'add failed'
    } finally {
      busy = false
    }
  }

  async function removeRule(rule: string) {
    if (category === 'miss' || busy) return

    const id = ++requestID
    const requestedCategory = category
    busy = true
    error = ''
    try {
      await api.removeRules(requestedCategory, [rule])
      if (!requestIsCurrent(id, requestedCategory)) return
      // The delete is persisted; release the lock and open the undo window
      // immediately, so a slow or failing reload can neither block the undo
      // button nor hide that the deletion succeeded.
      busy = false
      clearTimeout(undoTimer)
      lastRemoved = rule
      undoTimer = setTimeout(() => (lastRemoved = null), 6000)
      await reloadView()
      listRef?.focus()
      if (category !== requestedCategory) return
      await refreshChanges()
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'remove failed'
    } finally {
      // reloadView() advances requestID, so the requestIsCurrent guard can
      // never clear busy after a successful delete; reset unconditionally —
      // the category-switch effect resets busy as well.
      busy = false
    }
  }

  async function undoRemove() {
    if (category === 'miss' || !lastRemoved || busy) return
    const rule = lastRemoved
    lastRemoved = null
    clearTimeout(undoTimer)

    busy = true
    error = ''
    try {
      await api.addRules(category, [rule])
      flashSaved('已恢复规则 ', rule)
      await reloadView()
      await refreshChanges()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'undo failed'
    } finally {
      busy = false
    }
  }

  async function resetCategory() {
    if (category === 'miss' || busy) return
    busy = true
    error = ''
    try {
      await api.resetRules(category)
      changesOpen = false
      flashSaved(`已重置${categoryName(category)}规则`)
      await reloadView()
      await refreshChanges()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'reset failed'
    } finally {
      busy = false
    }
  }

  // Customization state: persisted deltas relative to the boot rule files,
  // plus transient save feedback.
  let changes = $state<RuleChangeSet | null>(null)
  let changesOpen = $state(false)
  let lastRemoved = $state<string | null>(null)
  let undoTimer: ReturnType<typeof setTimeout> | undefined
  // savedMessage carries the success feedback; its rule part renders
  // monospace so rule text matches the rest of the console.
  let savedMessage = $state<{ action: string; rule?: string } | null>(null)
  let savedTimer: ReturnType<typeof setTimeout> | undefined

  const categoryDelta = $derived(category === 'miss' ? undefined : changes?.rules[category])
  const changeCount = $derived((categoryDelta?.add.length ?? 0) + (categoryDelta?.remove.length ?? 0))

  async function refreshChanges() {
    try {
      changes = await api.rulesChanges()
    } catch (e) {
      // The badge is auxiliary; only auth expiry is worth acting on.
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
    }
  }

  function flashSaved(action: string, rule?: string) {
    clearTimeout(savedTimer)
    savedMessage = { action, rule }
    savedTimer = setTimeout(() => (savedMessage = null), 2500)
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

  function categoryName(c: Category | 'miss'): string {
    switch (c) {
      case 'block':
        return '拦截'
      case 'direct':
        return '直连'
      case 'proxy':
        return '代理'
      default:
        return '未命中'
    }
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

  // Reset and reload whenever the breadcrumb-driven category changes. The
  // load/refresh calls are untracked: they read query/offset/sortBy, and a
  // tracked $effect would re-run (and reset the list) on every sort or
  // search change — Svelte 5 tracks reads through the synchronous part of
  // async callees.
  $effect(() => {
    category
    clearTimeout(searchTimer)
    clearTimeout(undoTimer)
    clearTimeout(savedTimer)
    rules = null
    total = 0
    offset = 0
    newRule = ''
    query = ''
    sortBy = 'default'
    sortDir = 'desc'
    error = ''
    busy = false
    lastRemoved = null
    changesOpen = false
    savedMessage = null
    missSort = 'count'
    missDomains = null
    missError = ''
    untrack(() => {
      // The miss view is global; the rules list and change badge only load
      // for real categories.
      if (category === 'miss') {
        void loadMiss()
      } else {
        void load(true)
        void refreshChanges()
      }
    })
  })

  async function loadMiss() {
    try {
      const resp = await api.ruleMiss(missSort, 20)
      missDomains = resp
      missError = ''
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      missError = e instanceof Error ? e.message : 'load failed'
    }
  }

  function setMissSort(sort: 'count' | 'recent') {
    if (missSort === sort) return
    missSort = sort
    missDomains = null
    void loadMiss()
  }

  function onSearchInput(event: Event) {
    query = (event.currentTarget as HTMLInputElement).value
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      offset = 0
      searching = true
      void load(true).finally(() => (searching = false))
    }, 300)
  }

  // Column header sorting cycles three states: the column's natural
  // direction, its reverse, then back to file order.
  const naturalDir: Record<'rule' | 'hits' | 'last_seen', RuleSortDir> = {
    rule: 'asc',
    hits: 'desc',
    last_seen: 'desc',
  }

  function toggleSort(col: 'rule' | 'hits' | 'last_seen') {
    if (sortBy !== col) {
      sortBy = col
      sortDir = naturalDir[col]
    } else if (sortDir === naturalDir[col]) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc'
    } else {
      sortBy = 'default'
    }
    offset = 0
    void load(true)
  }

  // aria-sort belongs only on the actively sorted header (ARIA practice).
  function ariaSort(col: 'rule' | 'hits' | 'last_seen'): 'ascending' | 'descending' | undefined {
    if (sortBy !== col) return undefined
    return sortDir === 'asc' ? 'ascending' : 'descending'
  }
</script>

{#snippet sortButton(col: 'rule' | 'hits' | 'last_seen', label: string)}
  <button
    type="button"
    class="inline-flex min-h-11 items-center gap-1 rounded-sm px-1 transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 sm:min-h-0 {sortBy === col
      ? 'text-foreground'
      : 'text-muted-foreground'}"
    title={`按${label}排序`}
    onclick={() => toggleSort(col)}
  >
    {label}
    {#if sortBy === col && sortDir === 'asc'}
      <ArrowUp class="size-3.5" />
    {:else if sortBy === col}
      <ArrowDown class="size-3.5" />
    {:else}
      <ChevronsUpDown class="size-3.5 opacity-50" />
    {/if}
  </button>
{/snippet}

{#if category !== 'miss'}
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
      <div class="grid gap-2 border-t pt-3" in:fade={{ duration: prefersReducedMotion.current ? 0 : 120 }}>
        <div class="flex items-center gap-2">
          <span class="text-sm text-muted-foreground">路由</span>
          <Badge variant={routeVariant(testResult.route)}>{routeLabels[testResult.route]}</Badge>
          <code class="min-w-0 break-all font-mono text-base font-medium">{testResult.domain}</code>
        </div>
        <div class="grid gap-1.5">
          {#each testResult.matches as m}
            <div class="flex items-center gap-2 text-sm {m.matched ? '' : 'opacity-60'}">
              <Badge variant={matchVariant(m.category, m.matched)}>
                {m.category}{m.matched ? '' : ' 未命中'}
              </Badge>
              {#if m.matched}
                <code class="min-w-0 break-all font-mono text-sm">{m.rule}</code>
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
{:else}
  <div class="mb-3 grid gap-2 sm:flex sm:items-center">
    <div class="relative min-w-0 sm:w-56">
      <Search class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={query}
        oninput={onSearchInput}
        placeholder="搜索规则…"
        aria-label="搜索规则"
        class="pl-8"
      />
    </div>
    <div class="flex min-w-0 flex-1 items-center gap-1.5 sm:max-w-md">
      <Input
        bind:value={newRule}
        placeholder="添加规则，如 **.example.com"
        aria-label="添加规则"
        class="min-w-0 font-mono"
        onkeydown={(e) => {
          if (e.key === 'Enter') void addRule()
        }}
      />
      <Button class="shrink-0 gap-1" onclick={addRule} disabled={busy || !newRule.trim()}>
        <Plus class="size-4" />
        添加
      </Button>
    </div>
    <div class="flex items-center gap-2 sm:ml-auto">
      {#if changeCount > 0}
        <button
          type="button"
          class="shrink-0 rounded-full bg-primary/10 px-2.5 py-0.5 text-xs font-medium text-primary transition-colors outline-none hover:bg-primary/20 focus-visible:ring-3 focus-visible:ring-ring/50"
          aria-expanded={changesOpen}
          onclick={() => (changesOpen = !changesOpen)}
        >
          自定义 +{categoryDelta?.add.length ?? 0} / −{categoryDelta?.remove.length ?? 0}
        </button>
      {/if}
      <Badge variant="secondary" class="ml-auto shrink-0">
        {#if rules.length === total}
          {formatCount(total)} 条规则
        {:else}
          {formatCount(rules.length)} / {formatCount(total)} 条规则
        {/if}
      </Badge>
    </div>
  </div>
  {#if changesOpen && categoryDelta}
    <div class="mb-3 rounded-lg border bg-card px-4 py-3" in:fade={{ duration: prefersReducedMotion.current ? 0 : 120 }}>
      <div class="grid gap-1.5">
        {#each categoryDelta.add as rule (rule)}
          <div class="flex items-center gap-2 text-sm">
            <span class="font-mono text-xs font-medium text-primary">+</span>
            <code class="min-w-0 break-all font-mono text-xs">{rule}</code>
          </div>
        {/each}
        {#each categoryDelta.remove as rule (rule)}
          <div class="flex items-center gap-2 text-sm">
            <span class="font-mono text-xs font-medium text-destructive">−</span>
            <code class="min-w-0 break-all font-mono text-xs line-through opacity-60">{rule}</code>
          </div>
        {/each}
      </div>
      <div class="mt-2.5 flex items-center gap-2 border-t pt-2.5">
        <p class="min-w-0 flex-1 text-xs text-muted-foreground">
          {#if changes?.persistent}
            自定义变更保存在 admin 状态文件,重启后仍然生效。
          {:else}
            变更仅保存在内存中,重启后丢失(未配置 admin.state_file)。
          {/if}
        </p>
        <Button variant="outline" size="sm" class="shrink-0 gap-1" onclick={resetCategory} disabled={busy}>
          <RotateCcw class="size-3.5" />
          重置本类
        </Button>
      </div>
    </div>
  {/if}
  {#if lastRemoved}
    <div
      class="mb-3 flex items-center gap-2 rounded-md border bg-card px-3 py-2 text-sm"
      in:fade={{ duration: prefersReducedMotion.current ? 0 : 120 }}
      role="status"
    >
      <span class="min-w-0 flex-1 truncate">
        已删除规则 <code class="font-mono text-xs">{lastRemoved}</code>，已保存。
      </span>
      <Button variant="ghost" size="sm" class="shrink-0 gap-1" onclick={undoRemove} disabled={busy}>
        <Undo2 class="size-3.5" />
        撤销
      </Button>
    </div>
  {:else if savedMessage}
    <div class="mb-3 flex items-center gap-1.5 px-1 text-sm text-primary" in:fade={{ duration: prefersReducedMotion.current ? 0 : 120 }} role="status">
      <Check class="size-4 shrink-0" />
      <span class="min-w-0 truncate">
        {savedMessage.action}{#if savedMessage.rule}<code class="font-mono text-xs">{savedMessage.rule}</code>{/if}
      </span>
    </div>
  {/if}
  {#if total === 0}
    <div class="flex flex-col items-center gap-2 py-10 text-sm text-muted-foreground">
      <ListX class="size-5" aria-hidden="true" />
      <p>{query.trim() ? `没有匹配“${query.trim()}”的规则。` : '该分类暂无规则。'}</p>
    </div>
  {:else}
    {#if searching}
      <p class="mb-2 text-xs text-muted-foreground" role="status">搜索中…</p>
    {/if}
    <Card.Card bind:ref={listRef} tabindex={-1} class="focus:outline-none">
      <Table.Table>
        <Table.TableCaption class="px-4 pb-1 text-left">
          命中统计自启动以来累计，重启后清零；升序可将零命中规则置顶，便于清理。
        </Table.TableCaption>
        <Table.TableHeader>
          <Table.TableRow class="hover:bg-transparent">
            <Table.TableHead scope="col" aria-sort={ariaSort('rule')}>
              {@render sortButton('rule', '规则')}
            </Table.TableHead>
            <Table.TableHead scope="col" class="text-right" aria-sort={ariaSort('hits')}>
              {@render sortButton('hits', '命中次数')}
            </Table.TableHead>
            <Table.TableHead scope="col" class="hidden text-right sm:table-cell" aria-sort={ariaSort('last_seen')}>
              {@render sortButton('last_seen', '最近命中')}
            </Table.TableHead>
            <Table.TableHead scope="col" class="w-0 text-right">
              <span class="sr-only">操作</span>
            </Table.TableHead>
          </Table.TableRow>
        </Table.TableHeader>
        <Table.TableBody>
          {#each rules as entry (entry.rule)}
            <Table.TableRow>
              <Table.TableCell class="w-full whitespace-normal">
                <code class="break-all font-mono text-sm">{entry.rule}</code>
              </Table.TableCell>
              <Table.TableCell class="text-right tabular-nums {entry.count === 0 ? 'text-muted-foreground' : ''}">
                {formatCount(entry.count)}
              </Table.TableCell>
              <Table.TableCell class="hidden text-right text-muted-foreground tabular-nums sm:table-cell">
                {entry.lastSeen ? formatTime(entry.lastSeen) : '—'}
              </Table.TableCell>
              <Table.TableCell class="text-right">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  class="size-11 text-muted-foreground hover:bg-destructive/10 hover:text-destructive sm:size-8"
                  aria-label={`删除 ${entry.rule}`}
                  title={`删除 ${entry.rule}`}
                  onclick={() => removeRule(entry.rule)}
                  disabled={busy}
                >
                  <Trash2 class="size-4" />
                </Button>
              </Table.TableCell>
            </Table.TableRow>
          {/each}
        </Table.TableBody>
      </Table.Table>
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
{/if}
{:else}
  <div class="mb-3 flex flex-wrap items-center gap-2">
    <div
      class="flex w-fit shrink-0 items-center gap-0.5 rounded-md border bg-muted/50 p-0.5 [&>button]:min-h-11 sm:[&>button]:min-h-0"
      role="group"
      aria-label="统计排序"
    >
      <button
        type="button"
        class="rounded-sm px-2.5 py-1 text-xs font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 {missSort === 'count'
          ? 'bg-card text-foreground shadow-sm'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={missSort === 'count'}
        onclick={() => setMissSort('count')}
      >
        最多访问
      </button>
      <button
        type="button"
        class="rounded-sm px-2.5 py-1 text-xs font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50 {missSort === 'recent'
          ? 'bg-card text-foreground shadow-sm'
          : 'text-muted-foreground hover:text-foreground'}"
        aria-pressed={missSort === 'recent'}
        onclick={() => setMissSort('recent')}
      >
        最近访问
      </button>
    </div>
    <p class="text-xs text-muted-foreground">
      未命中任何 block/direct/proxy 规则的连接域名（检测直连或回退代理），自启动以来累计。
    </p>
  </div>
  {#if missError}
    <p class="text-sm text-destructive">{missError}</p>
  {:else if missDomains === null}
    <Loading />
  {:else if missDomains.length === 0}
    <div class="flex flex-col items-center gap-2 rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
      <Inbox class="size-5" aria-hidden="true" />
      <p>暂无未命中规则的连接。</p>
    </div>
  {:else}
    <Card.Card>
      <ol class="divide-y px-4 py-1">
        {#each missDomains as m, i (m.rule)}
          <li class="flex items-center gap-2 py-1.5 text-sm">
            <span class="w-6 shrink-0 text-right tabular-nums text-muted-foreground">{i + 1}</span>
            <code class="min-w-0 flex-1 break-all font-mono">{m.rule}</code>
            <span class="shrink-0 tabular-nums {m.count === 0 ? 'text-muted-foreground' : ''}">
              {formatCount(m.count)} 次
            </span>
            <span class="hidden shrink-0 text-muted-foreground tabular-nums sm:inline">
              {m.lastSeen ? formatTime(m.lastSeen) : '—'}
            </span>
          </li>
        {/each}
      </ol>
    </Card.Card>
  {/if}
{/if}
