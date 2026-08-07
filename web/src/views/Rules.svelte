<script lang="ts">
  import { fade } from 'svelte/transition'
  import { api, ApiError, type Category, type DomainTest, type RuleChangeSet } from '$lib/api'
  import { formatCount } from '$lib/format'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { Check, CircleAlert, ListX, Plus, RotateCcw, Search, Trash2, Undo2 } from 'lucide-svelte'

  let { category, onUnauthorized }: { category: Category; onUnauthorized: () => void } = $props()

  const pageSize = 200

  let rules = $state<string[] | null>(null)
  let total = $state(0)
  let offset = $state(0)
  let newRule = $state('')
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

  async function addRule() {
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
      await reloadView()
      if (category !== requestedCategory) return
      await refreshChanges()
      flashSaved()
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
    if (busy) return

    const id = ++requestID
    const requestedCategory = category
    busy = true
    error = ''
    try {
      await api.removeRules(requestedCategory, [rule])
      if (!requestIsCurrent(id, requestedCategory)) return
      await reloadView()
      if (category !== requestedCategory) return
      // The delete is already persisted; offer a short undo window.
      clearTimeout(undoTimer)
      lastRemoved = rule
      undoTimer = setTimeout(() => (lastRemoved = null), 6000)
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
    if (!lastRemoved || busy) return
    const rule = lastRemoved
    lastRemoved = null
    clearTimeout(undoTimer)

    busy = true
    error = ''
    try {
      await api.addRules(category, [rule])
      await reloadView()
      await refreshChanges()
      flashSaved()
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
    if (busy) return
    busy = true
    error = ''
    try {
      await api.resetRules(category)
      changesOpen = false
      await reloadView()
      await refreshChanges()
      flashSaved()
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
  let savedFlash = $state(false)

  const categoryDelta = $derived(changes?.rules[category])
  const changeCount = $derived((categoryDelta?.add.length ?? 0) + (categoryDelta?.remove.length ?? 0))

  async function refreshChanges() {
    try {
      changes = await api.rulesChanges()
    } catch (e) {
      // The badge is auxiliary; only auth expiry is worth acting on.
      if (e instanceof ApiError && e.status === 401) onUnauthorized()
    }
  }

  function flashSaved() {
    savedFlash = true
    setTimeout(() => (savedFlash = false), 2500)
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
    clearTimeout(undoTimer)
    rules = null
    total = 0
    offset = 0
    newRule = ''
    query = ''
    error = ''
    busy = false
    lastRemoved = null
    changesOpen = false
    void load(true)
    void refreshChanges()
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
      <div class="grid gap-2 border-t pt-3" in:fade={{ duration: 120 }}>
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
          class="shrink-0 rounded-full bg-primary/10 px-2.5 py-0.5 text-xs font-medium text-primary transition-colors hover:bg-primary/20"
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
    <div class="mb-3 rounded-lg border bg-card px-4 py-3" in:fade={{ duration: 120 }}>
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
      in:fade={{ duration: 120 }}
      role="status"
    >
      <span class="min-w-0 flex-1 truncate">
        已删除 <code class="font-mono text-xs">{lastRemoved}</code>,已保存。
      </span>
      <Button variant="ghost" size="sm" class="shrink-0 gap-1" onclick={undoRemove} disabled={busy}>
        <Undo2 class="size-3.5" />
        撤销
      </Button>
    </div>
  {:else if savedFlash}
    <div class="mb-3 flex items-center gap-1.5 px-1 text-sm text-primary" in:fade={{ duration: 120 }} role="status">
      <Check class="size-4" />
      已保存
    </div>
  {/if}
  {#if total === 0}
    <div class="flex flex-col items-center gap-2 py-10 text-sm text-muted-foreground">
      <ListX class="size-5" aria-hidden="true" />
      <p>{query.trim() ? `没有匹配“${query.trim()}”的规则。` : '该分类暂无规则。'}</p>
    </div>
  {:else}
    <Card.Card>
      <Card.CardContent class="divide-y p-0">
        {#each rules as rule (rule)}
          <div class="flex items-center justify-between gap-3 px-4 py-1.5 transition-colors hover:bg-muted/40">
            <code class="min-w-0 break-all font-mono text-sm">{rule}</code>
            <Button
              variant="ghost"
              size="icon-sm"
              class="size-11 shrink-0 text-muted-foreground hover:bg-destructive/10 hover:text-destructive sm:size-8"
              aria-label={`删除 ${rule}`}
              title={`删除 ${rule}`}
              onclick={() => removeRule(rule)}
              disabled={busy}
            >
              <Trash2 class="size-4" />
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
{/if}
