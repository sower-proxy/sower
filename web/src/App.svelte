<script lang="ts">
  import { fade } from 'svelte/transition'
  import Login from './views/Login.svelte'
  import Overview from './views/Overview.svelte'
  import Rules from './views/Rules.svelte'
  import Traffic from './views/Traffic.svelte'
  import BreadcrumbHeader from './lib/components/BreadcrumbHeader.svelte'
  import { navItems, type BreadcrumbContextSegment, type NavKey } from './lib/navigation'
  import { api, ApiError, type Category, type Status } from './lib/api'
  import { startPolling } from './lib/poll'

  let authed = $state(false)
  let activeNav: NavKey = $state('overview')
  let ruleCategory: Category = $state('proxy')
  let status: Status | null = $state(null)
  let statusError = $state('')
  let stopStatusPoll: (() => void) | undefined

  async function refreshStatus() {
    if (!authed) return
    try {
      status = await api.status()
      statusError = ''
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      statusError = e instanceof Error ? e.message : 'status failed'
    }
  }

  const activeItem = $derived(navItems.find((item) => item.key === activeNav) ?? navItems[0]!)

  const contextSegments = $derived.by((): BreadcrumbContextSegment[] => {
    switch (activeNav) {
      case 'overview':
        if (!status) return []
        return [
          {
            label: 'version',
            value: status.version || '-',
            tone: 'strong',
            title: `构建日期 ${status.date || '未知'}`,
          },
        ]
      case 'rules':
        return [
          {
            label: 'category',
            value: ruleCategory,
            tone: 'strong',
            options: (['block', 'direct', 'proxy'] as Category[]).map((c) => ({
              label: c,
              value: c,
              active: c === ruleCategory,
            })),
          },
        ]
      case 'traffic':
        return [{ label: 'scope', value: 'top 100', tone: 'strong' }]
    }
  })

  function onLogin() {
    authed = true
    activeNav = 'overview'
    status = null
    statusError = ''
    stopStatusPoll = startPolling(refreshStatus, 5000)
  }

  async function handleLogout() {
    try {
      await api.logout()
    } catch {
      // Local auth state must reset even if the session revoke request fails.
    } finally {
      onUnauthorized()
    }
  }

  function onUnauthorized() {
    authed = false
    status = null
    statusError = ''
    stopStatusPoll?.()
    stopStatusPoll = undefined
  }

  function handleContextSelect(label: string, value: string) {
    if (label === 'category') {
      ruleCategory = value as Category
    }
  }
</script>

{#if !authed}
  <Login {onLogin} />
{:else}
  <BreadcrumbHeader
    {activeItem}
    {contextSegments}
    onContextSelect={handleContextSelect}
    onSelectItem={(key) => (activeNav = key)}
    {status}
    {statusError}
    onLogout={handleLogout}
  />
  <main>
    {#key activeNav}
      <div in:fade={{ duration: 120 }}>
        {#if activeNav === 'overview'}
          <Overview {status} {onUnauthorized} />
        {:else if activeNav === 'rules'}
          <Rules category={ruleCategory} {onUnauthorized} />
        {:else}
          <Traffic {onUnauthorized} />
        {/if}
      </div>
    {/key}
  </main>
{/if}

<style>
  main {
    max-width: 1100px;
    margin: 0 auto;
    padding: 1.25rem 1rem 3rem;
  }
</style>
