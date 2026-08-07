<script lang="ts">
  import { fade } from 'svelte/transition'
  import Login from './views/Login.svelte'
  import Overview from './views/Overview.svelte'
  import Rules from './views/Rules.svelte'
  import Traffic from './views/Traffic.svelte'
  import Config from './views/Config.svelte'
  import BreadcrumbHeader from './lib/components/BreadcrumbHeader.svelte'
  import { navItems, type BreadcrumbContextSegment, type NavKey } from './lib/navigation'
  import { api, type Category, type Source } from './lib/api'
  import { closeLive, connectLive, live, setUnauthorizedHandler } from './lib/live.svelte.ts'

  let authed = $state(false)
  let activeNav: NavKey = $state('overview')
  let ruleCategory: Category = $state('proxy')
  let trafficSource: Source = $state('all')

  // Status, traffic, and history come from the shared SSE stream; App only
  // owns the connection lifecycle and the session.
  const status = $derived(live.status)

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
            label: '分类',
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
        return [
          {
            label: 'source',
            value: trafficSource,
            tone: 'strong',
            options: (['all', 'http', 'https', 'socks5', 'dns'] as Source[]).map((s) => ({
              label: s,
              value: s,
              active: s === trafficSource,
            })),
          },
        ]
      case 'config':
        return []
    }
  })

  function onLogin() {
    authed = true
    activeNav = 'overview'
    connectLive()
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
    closeLive()
  }

  // Route the stream's session-expiry event to the logout flow.
  $effect(() => {
    setUnauthorizedHandler(onUnauthorized)
  })

  function handleContextSelect(label: string, value: string) {
    if (label === '分类') {
      ruleCategory = value as Category
    } else if (label === 'source') {
      trafficSource = value as Source
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
    onLogout={handleLogout}
  />
  <main>
    {#key activeNav}
      <div in:fade={{ duration: 120 }}>
        {#if activeNav === 'overview'}
          <Overview {status} />
        {:else if activeNav === 'rules'}
          <Rules category={ruleCategory} {onUnauthorized} />
        {:else if activeNav === 'traffic'}
          <Traffic source={trafficSource} />
        {:else}
          <Config {onUnauthorized} />
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
