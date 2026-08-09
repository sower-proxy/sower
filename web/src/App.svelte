<script lang="ts">
  import { onMount } from 'svelte'
  import { fade } from 'svelte/transition'
  import { prefersReducedMotion } from 'svelte/motion'
  import Login from './views/Login.svelte'
  import Overview from './views/Overview.svelte'
  import Rules from './views/Rules.svelte'
  import Traffic from './views/Traffic.svelte'
  import Config from './views/Config.svelte'
  import BreadcrumbHeader from './lib/components/BreadcrumbHeader.svelte'
  import { navItems, type BreadcrumbContextSegment, type NavKey } from './lib/navigation'
  import { ApiError, api, probeSession, type Category, type Source } from './lib/api'
  import { closeLive, connectLive, live, setUnauthorizedHandler } from './lib/live.svelte.ts'
  import Button from '$lib/components/ui/button/button.svelte'
  import { RefreshCw } from 'lucide-svelte'

  let authed = $state(false)
  let checkingSession = $state(true)
  let sessionError = $state('')
  // --- URL routing ---
  // The active view lives in the path (/rules, /traffic, /config) so
  // refresh, back/forward and bookmarks keep the page. Sub-menu state
  // (rules category, traffic source) rides in the second segment:
  // /rules/block, /traffic/http. Unknown paths resolve to the overview.
  function navFromPath(): { nav: NavKey; category?: Category | 'miss'; source?: Source } {
    const p = window.location.pathname.replace(/\/+$/, '')
    const [base, sub] = p.split('/').filter(Boolean)
    switch (base) {
      case 'rules': {
        const category = (['block', 'direct', 'proxy', 'miss'] as const).includes(sub as never)
          ? (sub as Category | 'miss')
          : undefined
        return { nav: 'rules', category }
      }
      case 'traffic': {
        const source = (['all', 'http', 'https', 'socks5', 'dns'] as const).includes(sub as never)
          ? (sub as Source)
          : undefined
        return { nav: 'traffic', source }
      }
      case 'config':
        return { nav: 'config' }
      default:
        return { nav: 'overview' }
    }
  }

  function pathForNav(nav: NavKey, category?: Category | 'miss', source?: Source): string {
    if (nav === 'rules' && category) return `/rules/${category}`
    if (nav === 'traffic' && source) return `/traffic/${source}`
    return nav === 'overview' ? '/' : `/${nav}`
  }

  // selectNav switches the view; an optional sub-state (from a favorite
  // shortcut) restores the rules category or traffic source. Without a
  // sub-state the previous sub-state is kept, so the URL keeps the last
  // used category/source segment and the view stays consistent with it.
  function selectNav(key: NavKey, sub?: { category?: Category | 'miss'; source?: Source }) {
    activeNav = key
    if (sub?.category) ruleCategory = sub.category
    if (sub?.source) trafficSource = sub.source
    history.pushState({}, '', pathForNav(key, ruleCategory, trafficSource))
  }

  let activeNav: NavKey = $state(navFromPath().nav)
  let ruleCategory: Category | 'miss' = $state('proxy')
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
            mobileHidden: true,
          },
        ]
      case 'rules':
        return [
          {
            label: '分类',
            value: ruleCategory,
            tone: 'strong',
            options: (['block', 'direct', 'proxy', 'miss'] as (Category | 'miss')[]).map((c) => ({
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
    checkingSession = false
    sessionError = ''
    // A deep link may arrive while unauthenticated: land on the page the
    // visitor asked for instead of forcing the overview.
    const r = navFromPath()
    activeNav = r.nav
    if (r.category) ruleCategory = r.category
    if (r.source) trafficSource = r.source
    connectLive()
  }

  async function restoreSession() {
    checkingSession = true
    sessionError = ''
    try {
      await probeSession()
      onLogin()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        onUnauthorized()
        return
      }
      authed = false
      sessionError = '无法验证当前登录状态。请检查服务连接后重试。'
    } finally {
      checkingSession = false
    }
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
    checkingSession = false
    sessionError = ''
    closeLive()
  }

  function onPopState() {
    const r = navFromPath()
    activeNav = r.nav
    if (r.category) ruleCategory = r.category
    if (r.source) trafficSource = r.source
  }

  onMount(() => {
    // Normalize an unknown or legacy path to the resolved view, then keep
    // the view in sync with back/forward navigation.
    const resolved = navFromPath()
    activeNav = resolved.nav
    if (resolved.category) ruleCategory = resolved.category
    if (resolved.source) trafficSource = resolved.source
    const expected = pathForNav(resolved.nav, resolved.category, resolved.source)
    if (window.location.pathname !== expected) {
      history.replaceState({}, '', expected)
    }
    window.addEventListener('popstate', onPopState)
    void restoreSession()
    return () => window.removeEventListener('popstate', onPopState)
  })

  // Route the stream's session-expiry event to the logout flow.
  $effect(() => {
    setUnauthorizedHandler(onUnauthorized)
    return () => {
      setUnauthorizedHandler(null)
      closeLive()
    }
  })

  function handleContextSelect(label: string, value: string) {
    if (label === '分类') {
      ruleCategory = value as Category | 'miss'
    } else if (label === 'source') {
      trafficSource = value as Source
    }
    history.pushState({}, '', pathForNav(activeNav, ruleCategory, trafficSource))
  }
</script>

{#if checkingSession}
  <div class="flex min-h-svh items-center justify-center p-4">
    <p class="text-sm text-muted-foreground">正在恢复登录状态…</p>
  </div>
{:else if sessionError}
  <div class="flex min-h-svh items-center justify-center p-4">
    <div class="grid w-full max-w-sm gap-4 text-center">
      <p class="text-sm text-muted-foreground">{sessionError}</p>
      <Button onclick={restoreSession}>
        <RefreshCw class="size-4" aria-hidden="true" />
        重试
      </Button>
    </div>
  </div>
{:else if !authed}
  <Login {onLogin} />
{:else}
  <BreadcrumbHeader
    {activeItem}
    {contextSegments}
    onContextSelect={handleContextSelect}
    onSelectItem={selectNav}
    currentCategory={ruleCategory}
    currentSource={trafficSource}
    {status}
    onLogout={handleLogout}
  />
  <main>
    <!-- Screen-reader page landmark: the breadcrumb already shows the
         active section visually, so the h1 stays visually hidden. -->
    <h1 class="sr-only">{activeItem.label}</h1>
    {#key activeNav}
      <div in:fade={{ duration: prefersReducedMotion.current ? 0 : 120 }}>
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
