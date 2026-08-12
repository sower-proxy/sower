<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { api, ApiError, type ConfigChanges, type ConfigField, type ConfigView } from '$lib/api'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { Check, CircleAlert, Gauge, Globe, ListChecks, Pencil, Radio, RefreshCw, RotateCcw, Search, Server, Settings, X } from 'lucide-svelte'

  let { onUnauthorized }: { onUnauthorized: () => void } = $props()

  let view = $state<ConfigView | null>(null)
  let loadError = $state('')
  let applyError = $state('')
  let appliedFlash = $state(false)
  let applying = $state(false)
  let flashMessage = $state('')
  let restarting = $state(false)
  let restartError = $state('')

  // Staged edits: config key -> new override value ('' clears the override,
  // reverting the field to the config-file value).
  let staged = $state<Record<string, string>>({})
  let editingKey = $state<string | null>(null)
  let editValue = $state('')
  // Row focus targets: committing/cancelling an edit removes the inline
  // controls, so focus is parked back on the row to keep the reading
  // position stable for keyboard users.
  let rowRefs = $state<Record<string, HTMLDivElement>>({})
  let flashTimer: ReturnType<typeof setTimeout> | undefined
  // restartAbort cancels the post-restart polling loop when the component is
  // destroyed, so a navigation away cannot leave background probes running
  // (or let a stale 401 from a dead page trigger a global logout).
  let restartAbort: AbortController | null = null

  const stagedCount = $derived(Object.keys(staged).length)

  // Field filter: narrows the rendered sections by key or value. Sections
  // that end up empty are hidden; the count line keeps the total visible.
  let filter = $state('')

  const filteredSections = $derived.by(() => {
    const q = filter.trim().toLowerCase()
    const sections = view?.sections ?? []
    if (!q) return sections
    return sections
      .map((sec) => ({
        ...sec,
        fields: sec.fields.filter(
          (f) => f.key.toLowerCase().includes(q) || (f.value ?? '').toLowerCase().includes(q),
        ),
      }))
      .filter((sec) => sec.fields.length > 0)
  })

  const totalFieldCount = $derived((view?.sections ?? []).reduce((n, s) => n + s.fields.length, 0))
  const filteredFieldCount = $derived(filteredSections.reduce((n, s) => n + s.fields.length, 0))

  // fieldByKey looks up the current effective field for a staged key, so the
  // diff bar can show old -> new.
  const fieldByKey = $derived.by(() => {
    const map = new Map<string, ConfigField>()
    for (const sec of view?.sections ?? []) {
      for (const f of sec.fields) map.set(f.key, f)
    }
    return map
  })

  // displayValue renders a field's value for display: list fields collapse
  // to counts, everything else shows the raw value (or an em dash when
  // empty).
  function displayValue(field: ConfigField | undefined, value: string | undefined): string {
    if (value === undefined || value === '') return '—'
    if (field?.type === 'list') return `${value.split('\n').length} 条`
    return value
  }

  async function reload() {
    try {
      view = await api.config()
      loadError = ''
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return onUnauthorized()
      loadError = e instanceof Error ? e.message : '加载配置失败'
    }
  }

  onMount(reload)

  onDestroy(() => {
    if (flashTimer) clearTimeout(flashTimer)
    restartAbort?.abort()
  })

  // commitEditFor stages the in-progress edit for a key. A value equal to
  // the effective value drops the staged change instead.
  function commitEditFor(key: string) {
    const field = fieldByKey.get(key)
    const next = editValue.trim()
    if (field && next === (field.value ?? '')) {
      // Back to the effective value: drop the staged change.
      const { [key]: _, ...rest } = staged
      staged = rest
    } else {
      staged = { ...staged, [key]: next }
    }
  }

  function commitEdit() {
    if (editingKey == null) return
    const key = editingKey
    commitEditFor(key)
    editingKey = null
    rowRefs[key]?.focus()
  }

  function startEdit(field: ConfigField) {
    // Commit any in-progress edit before switching rows, so typed input is
    // never silently discarded.
    if (editingKey != null && editingKey !== field.key) commitEditFor(editingKey)
    editingKey = field.key
    editValue = staged[field.key] === '' ? (field.value ?? '') : (staged[field.key] ?? field.value ?? '')
    applyError = ''
  }

  function cancelEdit() {
    if (editingKey == null) return
    const key = editingKey
    editingKey = null
    rowRefs[key]?.focus()
  }

  function restoreConfigValue(field: ConfigField) {
    staged = { ...staged, [field.key]: '' }
    applyError = ''
  }

  async function apply() {
    if (!view) return
    // Commit any in-progress edit so it is not silently excluded.
    if (editingKey != null) {
      commitEditFor(editingKey)
      editingKey = null
    }
    if (stagedCount === 0) return
    applying = true
    applyError = ''
    try {
      // List fields stage as newline-joined text; the PATCH payload wants
      // the split lists. Payload keys use underscores while display keys
      // are dotted (dns.upstream -> dns_upstream).
      const changes: Record<string, string | string[]> = {}
      for (const [key, next] of Object.entries(staged)) {
        const field = fieldByKey.get(key)
        const changeKey = key.replaceAll('.', '_')
        changes[changeKey] = field?.type === 'list' ? next.split('\n').map((s) => s.trim()).filter(Boolean) : next
      }
      const hasRestartField = Object.keys(staged).some((k) => fieldByKey.get(k)?.applyMode === 'restart')
      view = await api.patchConfig(view.revision, changes as ConfigChanges)
      staged = {}
      appliedFlash = true
      flashMessage = hasRestartField ? '已保存，重启后生效。' : '已应用并保存，立即生效。'
      flashTimer = setTimeout(() => (appliedFlash = false), 2500)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return onUnauthorized()
      if (e instanceof ApiError && e.status === 409) {
        applyError = '配置已被其它会话修改，已载入最新值，请确认后重新应用。'
        await reload()
      } else {
        applyError = e instanceof Error ? e.message : '应用失败'
      }
    } finally {
      applying = false
    }
  }

  function discardStaged() {
    staged = {}
    applyError = ''
  }

  // confirmRestart asks for confirmation, triggers the in-place process
  // restart, then polls until the service answers again. The process
  // replaces itself with the same PID, so the session cookie survives.
  async function confirmRestart() {
    if (restarting) return
    if (!confirm('重启 sower 服务使配置生效？代理连接会短暂中断。')) return
    restarting = true
    restartError = ''
    restartAbort = new AbortController()
    const signal = restartAbort.signal
    try {
      await api.restart()
      const deadline = Date.now() + 60_000
      for (;;) {
        await new Promise((r) => setTimeout(r, 1000))
        if (signal.aborted) return // component destroyed; stop polling
        // The in-place exec restart keeps the old keep-alive sockets alive
        // but unserved; bound each probe so a stale connection cannot hang
        // the poll forever.
        const controller = new AbortController()
        const timer = setTimeout(() => controller.abort(), 2000)
        try {
          const resp = await fetch('/api/session', { signal: controller.signal, cache: 'no-store' })
          // The session probe answers 204 once the service is back; 401
          // means the process restarted but the cookie is gone (re-login
          // needed). Both prove the service is responding again.
          if (resp.status === 204 || resp.status === 401) break
        } catch {
          // connection refused or stale keep-alive while the process is down
        } finally {
          clearTimeout(timer)
        }
        if (Date.now() > deadline) {
          restartError = '服务 60 秒内未恢复，请检查服务状态。'
          break
        }
      }
      if (signal.aborted) return
      if (!restartError) {
        await reload()
        staged = {}
        appliedFlash = true
        flashMessage = '服务已重启，配置已生效。'
        flashTimer = setTimeout(() => (appliedFlash = false), 2500)
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return onUnauthorized()
      restartError = e instanceof Error ? e.message : '重启失败'
    } finally {
      restartAbort = null
      restarting = false
    }
  }

  // groupFields splits a section's fields into sub-groups by their dotted
  // key prefix (router.block.* -> "block"), so repetitive field families get
  // sub-headings instead of a flat list. Fields without a dotted prefix stay
  // in one unlabeled group.
  function groupFields(fields: ConfigField[]): { label: string; fields: ConfigField[] }[] {
    const groups: { label: string; fields: ConfigField[] }[] = []
    for (const f of fields) {
      const parts = f.key.split('.')
      let label = ''
      if (parts.length >= 2) {
        label = parts[0] === 'router' ? (parts[1] ?? '') : (parts[0] ?? '')
      }
      const g = groups.find((g) => g.label === label)
      if (g) g.fields.push(f)
      else groups.push({ label, fields: [f] })
    }
    return groups
  }

  const modeLabel: Record<string, string> = {
    immediate: '立即生效',
    restart: '重启生效',
    readonly: '只读',
  }

  // Section identity: a per-section icon (muted, per the theme's "teal is
  // for actions" rule) and a wide flag. Sections with many fields span the
  // full grid width so rows keep room for key + value + badges.
  const sectionIcon: Record<string, typeof Gauge> = {
    运行: Gauge,
    远程代理: Server,
    DNS: Globe,
    监听: Radio,
    规则来源: ListChecks,
  }
  const isWideSection = (fields: ConfigField[]) => fields.length >= 8
</script>

{#if !view}
  {#if loadError}
    <Alert.Alert variant="destructive">
      <CircleAlert class="size-4" />
      <Alert.AlertDescription>{loadError}</Alert.AlertDescription>
    </Alert.Alert>
  {:else}
    <Loading />
  {/if}
{:else}
  <div class="mb-4 flex flex-wrap items-center gap-2">
    <div class="relative w-full max-w-xs">
      <Search
        class="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
        aria-hidden="true"
      />
      <Input bind:value={filter} placeholder="过滤字段（key 或值）" aria-label="过滤字段" class="pl-8" />
    </div>
    {#if filter}
      <Button variant="ghost" size="sm" aria-label="清除过滤" onclick={() => (filter = '')}>清除</Button>
    {/if}
    <span class="text-xs text-muted-foreground tabular-nums">
      {filter ? `${filteredFieldCount} / ${totalFieldCount}` : totalFieldCount} 个字段
    </span>
    <div class="ml-auto">
      <Button variant="outline" size="sm" class="gap-1" onclick={confirmRestart} disabled={restarting}>
        <RefreshCw class="size-3.5" aria-hidden="true" />
        {restarting ? '重启中…' : '重启服务'}
      </Button>
    </div>
  </div>

  {#if filteredSections.length === 0}
    <p class="py-10 text-center text-sm text-muted-foreground">没有匹配「{filter.trim()}」的字段</p>
  {:else}
    <div class="grid items-start gap-4 lg:grid-cols-2">
      {#each filteredSections as section}
        {@const Icon = sectionIcon[section.name] ?? Settings}
        <Card.Card class={isWideSection(section.fields) ? 'lg:col-span-2' : ''}>
          <Card.CardHeader>
            <div class="flex items-center justify-between gap-2">
              <Card.CardTitle class="text-base" role="heading" aria-level={2}>
                <span class="inline-flex items-center gap-2">
                  <Icon class="size-4 text-muted-foreground" aria-hidden="true" />
                  {section.name}
                </span>
              </Card.CardTitle>
              <Badge variant="secondary" class="tabular-nums" title="字段数">
                {section.fields.length}
              </Badge>
            </div>
          </Card.CardHeader>
          <Card.CardContent>
            {@const groups = groupFields(section.fields)}
            {#each groups as group, gi}
              {#if group.label && groups.length > 1 && group.fields.length >= 2}
                <p
                  role="heading"
                  aria-level={3}
                  class="pb-1.5 pt-3 text-xs font-semibold text-muted-foreground {gi > 0
                    ? 'mt-3 border-t border-border/60 pt-2.5'
                    : ''}"
                >
                  {group.label}
                </p>
              {/if}
              {#each group.fields as field, fi (field.key)}
                {@const stagedValue = staged[field.key]}
                <div
                  bind:this={rowRefs[field.key]}
                  tabindex="-1"
                  class="flex flex-col gap-1.5 rounded-md py-2 first:pt-0 last:pb-0 transition-colors focus-visible:bg-muted/60 focus:outline-none sm:flex-row sm:items-center sm:gap-2 {fi > 0
                    ? 'border-t border-border/60'
                    : ''} {field.editable ? 'hover:bg-muted/40' : ''}"
                >
                  <span
                    class="truncate font-mono text-xs text-muted-foreground sm:w-40 sm:shrink-0"
                    title={field.key}
                  >
                    {field.key}
                  </span>
                  <div class="min-w-0 flex-1">
                    {#if field.secret}
                      <Badge variant="secondary">{field.configured ? '已配置' : '未配置'}</Badge>
                    {:else if field.type === 'bool'}
                      <Badge variant={field.value === 'true' ? 'secondary' : 'outline'}>
                        {field.value === 'true' ? '是' : '否'}
                      </Badge>
                    {:else if editingKey === field.key}
                      {#if field.type === 'list'}
                        <div class="flex items-start gap-1">
                          <textarea
                            bind:value={editValue}
                            aria-label={field.key}
                            placeholder={field.constraint ?? ''}
                            rows={Math.min(6, Math.max(2, editValue.split('\n').length))}
                            class="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1.5 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                            onkeydown={(e) => {
                              if (e.key === 'Escape') cancelEdit()
                              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) commitEdit()
                            }}
                          ></textarea>
                          <div class="flex shrink-0 flex-col gap-1">
                            <Button variant="ghost" size="icon" class="size-11 sm:size-8" aria-label="确认修改" onclick={commitEdit}>
                              <Check class="size-4 text-primary" />
                            </Button>
                            <Button variant="ghost" size="icon" class="size-11 sm:size-8" aria-label="取消修改" onclick={cancelEdit}>
                              <X class="size-4 text-muted-foreground" />
                            </Button>
                          </div>
                        </div>
                      {:else}
                        <div class="flex items-center gap-1">
                        {#if field.type === 'enum' && field.options?.length}
                          <select
                            value={editValue}
                            aria-label={field.key}
                            onchange={(e) => {
                              editValue = (e.currentTarget as HTMLSelectElement).value
                              commitEdit()
                            }}
                            class="h-8 w-full rounded-md border border-input bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                          >
                            {#each field.options as opt}
                              <option value={opt}>{opt}</option>
                            {/each}
                          </select>
                        {:else}
                          <Input
                            bind:value={editValue}
                            aria-label={field.key}
                            placeholder={field.constraint ?? ''}
                            class="h-8 font-mono text-sm"
                            onkeydown={(e) => {
                              if (e.key === 'Enter') commitEdit()
                              if (e.key === 'Escape') cancelEdit()
                            }}
                          />
                        {/if}
                        <Button variant="ghost" size="icon" class="size-11 shrink-0 sm:size-8" aria-label="确认修改" onclick={commitEdit}>
                          <Check class="size-4 text-primary" />
                        </Button>
                        <Button variant="ghost" size="icon" class="size-11 shrink-0 sm:size-8" aria-label="取消修改" onclick={cancelEdit}>
                          <X class="size-4 text-muted-foreground" />
                        </Button>
                      </div>
                      {/if}
                      {#if field.constraint}
                        <p class="mt-1 text-xs text-muted-foreground">{field.constraint}</p>
                      {/if}
                    {:else}
                      <span class="font-mono text-sm break-all">
                        {#if stagedValue !== undefined}
                          <span class="text-muted-foreground line-through">{displayValue(field, field.value)}</span>
                          <span class="ml-1 text-primary">{stagedValue === '' ? '恢复配置' : displayValue(field, stagedValue)}</span>
                        {:else}
                          {displayValue(field, field.value)}
                        {/if}
                      </span>
                      {#if field.editable && field.constraint}
                        <p class="mt-0.5 text-xs text-muted-foreground">{field.constraint}</p>
                      {/if}
                    {/if}
                  </div>
                  <div class="flex shrink-0 flex-wrap items-center gap-1">
                    {#if stagedValue !== undefined}
                      <Badge class="bg-primary/10 text-primary">待应用</Badge>
                    {:else if field.source === 'override'}
                      <Badge class="bg-primary/10 text-primary" title="该值来自控制台修改并持久化，优先于配置文件">页面覆盖</Badge>
                    {/if}
                    {#if field.applyMode !== 'immediate'}
                      <Badge
                        variant="secondary"
                        class="text-muted-foreground"
                        title={field.applyMode === 'readonly' ? '该字段不可通过控制台修改' : undefined}
                      >
                        {modeLabel[field.applyMode]}
                      </Badge>
                    {/if}
                    {#if field.editable && field.source === 'override' && stagedValue === undefined && editingKey !== field.key}
                      <Button
                        variant="ghost"
                        size="icon"
                        class="size-11 sm:size-8"
                        aria-label={`恢复 ${field.key} 配置`}
                        title="恢复配置文件值"
                        onclick={() => restoreConfigValue(field)}
                      >
                        <RotateCcw class="size-3.5 text-muted-foreground" />
                      </Button>
                    {/if}
                    {#if field.editable && editingKey !== field.key}
                      <Button
                        variant="ghost"
                        size="icon"
                        class="size-11 sm:size-8"
                        aria-label={`编辑 ${field.key}`}
                        onclick={() => startEdit(field)}
                      >
                        <Pencil class="size-3.5 text-muted-foreground" />
                      </Button>
                    {/if}
                  </div>
                </div>
              {/each}
            {/each}
          </Card.CardContent>
        </Card.Card>
      {/each}
    </div>
  {/if}

  {#if stagedCount > 0 || applyError || appliedFlash || restartError}
    <div
      class="sticky bottom-0 mt-4 flex flex-wrap items-center gap-2 rounded-lg border bg-card/95 px-3 py-2.5 shadow-sm backdrop-blur"
    >
      <span role="status" class="flex min-w-0 flex-wrap items-center gap-2">
        {#if appliedFlash && stagedCount === 0}
          <Check class="size-4 shrink-0 text-primary" aria-hidden="true" />
          <span class="text-sm text-primary">{flashMessage}</span>
        {:else}
          <span class="text-sm font-medium">{stagedCount} 项待应用：</span>
          {#each Object.entries(staged) as [key, next] (key)}
            {@const field = fieldByKey.get(key)}
            <Badge variant="secondary" class="font-mono text-xs">
              {key}: {displayValue(field, field?.value)} → {next === '' ? '恢复配置' : displayValue(field, next)}
            </Badge>
          {/each}
        {/if}
        {#if applyError}
          <span class="inline-flex items-center gap-1 text-sm text-destructive">
            <CircleAlert class="size-4" aria-hidden="true" />
            {applyError}
          </span>
        {/if}
        {#if restartError}
          <span class="inline-flex items-center gap-1 text-sm text-destructive">
            <CircleAlert class="size-4" aria-hidden="true" />
            {restartError}
          </span>
        {/if}
      </span>
      {#if stagedCount > 0}
        <div class="ml-auto flex items-center gap-1.5">
          <Button variant="ghost" size="sm" class="gap-1" onclick={discardStaged} disabled={applying}>
            <RotateCcw class="size-3.5" />
            放弃
          </Button>
          <Button size="sm" onclick={apply} disabled={applying}>
            {applying ? '应用中…' : '应用更改'}
          </Button>
        </div>
      {/if}
    </div>
  {/if}

  <p class="mt-3 text-xs text-muted-foreground">
    标注「立即生效」的字段可在线调整并持久化到 admin 状态文件；其余字段来自配置文件，需重启生效。密钥永不回显。
  </p>
{/if}
