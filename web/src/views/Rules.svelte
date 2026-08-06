<script lang="ts">
  import { api, ApiError, type Category, type RulesResponse } from '$lib/api'
  import { formatCount } from '$lib/format'
  import * as Card from '$lib/components/ui/card'
  import * as Alert from '$lib/components/ui/alert'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Textarea from '$lib/components/ui/textarea/textarea.svelte'
  import { CircleAlert } from 'lucide-svelte'

  let { category, onUnauthorized }: { category: Category; onUnauthorized: () => void } = $props()

  let rules = $state<string[] | null>(null)
  let draft = $state('')
  let error = $state('')
  let busy = $state(false)
  let requestID = 0

  function requestIsCurrent(id: number, requestedCategory: Category) {
    return id === requestID && requestedCategory === category
  }

  async function load() {
    const id = ++requestID
    const requestedCategory = category
    try {
      const resp: RulesResponse = await api.rules(requestedCategory)
      if (!requestIsCurrent(id, requestedCategory)) return
      rules = resp.rules
      error = ''
    } catch (e) {
      if (!requestIsCurrent(id, requestedCategory)) return
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      error = e instanceof Error ? e.message : 'load failed'
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
      const resp = await api.addRules(requestedCategory, items)
      if (!requestIsCurrent(id, requestedCategory)) return
      rules = resp.rules
      draft = ''
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
      const resp = await api.removeRules(requestedCategory, [rule])
      if (!requestIsCurrent(id, requestedCategory)) return
      rules = resp.rules
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

  // Reload whenever the breadcrumb-driven category changes.
  $effect(() => {
    category
    rules = null
    error = ''
    busy = false
    void load()
  })
</script>

{#if error}
  <Alert.Alert variant="destructive" class="mb-4">
    <CircleAlert class="size-4" />
    <Alert.AlertDescription>{error}</Alert.AlertDescription>
  </Alert.Alert>
{/if}

<Card.Card class="mb-4">
  <Card.CardContent class="grid gap-3 pt-6">
    <Textarea
      bind:value={draft}
      rows={3}
      placeholder={'One rule per line, e.g. example.com or **.cn\nRules take effect immediately and reset on restart.'}
    />
    <div class="flex gap-2">
      <Button onclick={addRules} disabled={busy || !draft.trim()}>Add rules</Button>
      <Button variant="outline" onclick={load} disabled={busy}>Refresh</Button>
    </div>
  </Card.CardContent>
</Card.Card>

{#if rules === null}
  <p class="py-8 text-center text-sm text-muted-foreground">Loading…</p>
{:else if rules.length === 0}
  <p class="py-8 text-center text-sm text-muted-foreground">No rules in this category.</p>
{:else}
  <div class="mb-2 flex items-center justify-between">
    <Badge variant="secondary">{formatCount(rules.length)} rules</Badge>
  </div>
  <Card.Card>
    <Card.CardContent class="divide-y p-0">
      {#each rules as rule (rule)}
        <div class="flex items-center justify-between gap-3 px-4 py-2.5">
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
{/if}
