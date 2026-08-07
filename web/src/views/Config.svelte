<script lang="ts">
  import { onMount } from 'svelte'
  import { api, ApiError, type ConfigChanges, type ConfigField, type ConfigView } from '$lib/api'
  import * as Card from '$lib/components/ui/card'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Input from '$lib/components/ui/input/input.svelte'
  import Loading from '$lib/components/Loading.svelte'
  import { Check, CircleAlert, Pencil, RotateCcw, X } from 'lucide-svelte'

  let { onUnauthorized }: { onUnauthorized: () => void } = $props()

  let view = $state<ConfigView | null>(null)
  let loadError = $state('')
  let applyError = $state('')
  let appliedFlash = $state(false)
  let applying = $state(false)

  // Staged edits: config key -> new override value ('' clears the override,
  // reverting the field to the config-file value).
  let staged = $state<Record<string, string>>({})
  let editingKey = $state<string | null>(null)
  let editValue = $state('')

  const logLevels = ['debug', 'info', 'warn', 'error']
  const stagedCount = $derived(Object.keys(staged).length)

  // fieldByKey looks up the current effective field for a staged key, so the
  // diff bar can show old -> new.
  const fieldByKey = $derived.by(() => {
    const map = new Map<string, ConfigField>()
    for (const sec of view?.sections ?? []) {
      for (const f of sec.fields) map.set(f.key, f)
    }
    return map
  })

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

  function startEdit(field: ConfigField) {
    editingKey = field.key
    editValue = staged[field.key] === '' ? (field.value ?? '') : (staged[field.key] ?? field.value ?? '')
    applyError = ''
  }

  function commitEdit() {
    if (editingKey == null) return
    const field = fieldByKey.get(editingKey)
    const next = editValue.trim()
    if (field && next === (field.value ?? '')) {
      // Back to the effective value: drop the staged change.
      const { [editingKey]: _, ...rest } = staged
      staged = rest
    } else {
      staged = { ...staged, [editingKey]: next }
    }
    editingKey = null
  }

  function cancelEdit() {
    editingKey = null
  }

  function restoreConfigValue(field: ConfigField) {
    staged = { ...staged, [field.key]: '' }
    applyError = ''
  }

  async function apply() {
    if (!view || stagedCount === 0) return
    applying = true
    applyError = ''
    try {
      const changes: ConfigChanges = { ...staged }
      view = await api.patchConfig(view.revision, changes)
      staged = {}
      appliedFlash = true
      setTimeout(() => (appliedFlash = false), 2500)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return onUnauthorized()
      if (e instanceof ApiError && e.status === 409) {
        applyError = '配置已被其它会话修改,已载入最新值,请确认后重新应用。'
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

  const modeLabel: Record<string, string> = {
    immediate: '立即生效',
    restart: '重启生效',
    readonly: '只读',
  }
</script>

{#if !view}
  {#if loadError}
    <div class="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
      <CircleAlert class="size-4" />
      {loadError}
    </div>
  {:else}
    <Loading />
  {/if}
{:else}
  <div class="grid items-start gap-4 lg:grid-cols-2">
    {#each view.sections as section}
      <Card.Card>
        <Card.CardHeader>
          <Card.CardTitle class="text-base">{section.name}</Card.CardTitle>
        </Card.CardHeader>
        <Card.CardContent class="divide-y divide-border/60">
          {#each section.fields as field (field.key)}
            {@const stagedValue = staged[field.key]}
            <div class="flex items-center gap-2 py-2 first:pt-0 last:pb-0">
              <span
                class="w-40 shrink-0 truncate font-mono text-xs text-muted-foreground"
                title={field.key}
              >
                {field.key}
              </span>
              <div class="min-w-0 flex-1">
                {#if field.secret}
                  <Badge variant="secondary">{field.configured ? '已配置' : '未配置'}</Badge>
                {:else if editingKey === field.key}
                  <div class="flex items-center gap-1">
                    {#if field.key === 'log_level'}
                      <select
                        bind:value={editValue}
                        aria-label={field.key}
                        class="h-8 w-full rounded-md border border-input bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                      >
                        {#each logLevels as lv}
                          <option value={lv}>{lv}</option>
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
                    <Button variant="ghost" size="icon" class="size-8 shrink-0" aria-label="确认修改" onclick={commitEdit}>
                      <Check class="size-4 text-primary" />
                    </Button>
                    <Button variant="ghost" size="icon" class="size-8 shrink-0" aria-label="取消修改" onclick={cancelEdit}>
                      <X class="size-4 text-muted-foreground" />
                    </Button>
                  </div>
                {:else}
                  <span class="font-mono text-sm break-all">
                    {#if stagedValue !== undefined}
                      <span class="text-muted-foreground line-through">{field.value || '—'}</span>
                      <span class="ml-1 text-primary">{stagedValue || '恢复配置'}</span>
                    {:else}
                      {field.value || '—'}
                    {/if}
                  </span>
                  {#if field.editable && field.constraint}
                    <p class="mt-0.5 text-[10px] text-muted-foreground">{field.constraint}</p>
                  {/if}
                {/if}
              </div>
              <div class="flex shrink-0 items-center gap-1">
                {#if stagedValue !== undefined}
                  <Badge class="bg-primary/10 text-primary">待应用</Badge>
                {:else if field.source === 'override'}
                  <Badge class="bg-primary/10 text-primary">页面覆盖</Badge>
                {/if}
                {#if field.applyMode !== 'immediate'}
                  <Badge variant="secondary" class="text-muted-foreground">{modeLabel[field.applyMode]}</Badge>
                {/if}
                {#if field.editable && field.source === 'override' && stagedValue === undefined && editingKey !== field.key}
                  <Button
                    variant="ghost"
                    size="icon"
                    class="size-8"
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
                    class="size-8"
                    aria-label={`编辑 ${field.key}`}
                    onclick={() => startEdit(field)}
                  >
                    <Pencil class="size-3.5 text-muted-foreground" />
                  </Button>
                {/if}
              </div>
            </div>
          {/each}
        </Card.CardContent>
      </Card.Card>
    {/each}
  </div>

  {#if stagedCount > 0 || applyError || appliedFlash}
    <div
      class="sticky bottom-0 mt-4 flex flex-wrap items-center gap-2 rounded-lg border bg-card/95 px-3 py-2.5 shadow-sm backdrop-blur"
      role="status"
    >
      {#if appliedFlash && stagedCount === 0}
        <Check class="size-4 text-primary" />
        <span class="text-sm text-primary">已应用并保存,立即生效。</span>
      {:else}
        <span class="text-sm font-medium">{stagedCount} 项待应用:</span>
        {#each Object.entries(staged) as [key, next] (key)}
          <Badge variant="secondary" class="font-mono text-xs">
            {key}: {fieldByKey.get(key)?.value || '—'} → {next || '恢复配置'}
          </Badge>
        {/each}
      {/if}
      {#if applyError}
        <span class="inline-flex items-center gap-1 text-sm text-destructive">
          <CircleAlert class="size-4" />
          {applyError}
        </span>
      {/if}
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
    标注「立即生效」的字段可在线调整并持久化到 admin 状态文件;其余字段来自配置文件,需重启生效。密钥永不回显。
  </p>
{/if}
