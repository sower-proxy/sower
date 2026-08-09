<script lang="ts">
  import { onMount, tick } from 'svelte'
  import { Activity, ChevronDown, CircleDot, Gauge, LayoutPanelTop, LogOut, Settings, SlidersHorizontal, Star } from 'lucide-svelte'
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import Logo from './Logo.svelte'
  import { navItems, type BreadcrumbContextSegment, type FavoritePage, type NavItem, type NavKey } from '../navigation'
  import type { Category, Source } from '$lib/api'
  import { cn } from '$lib/utils'

  let {
    activeItem,
    contextSegments = [],
    onContextSelect = () => {},
    onSelectItem = () => {},
    onLogout = () => {},
    status = null,
    statusError = '',
    currentCategory = undefined,
    currentSource = undefined,
  }: {
    activeItem: NavItem
    contextSegments?: BreadcrumbContextSegment[]
    onContextSelect?: (label: string, value: string) => void
    onSelectItem?: (key: NavKey, sub?: { category?: Category | 'miss'; source?: Source }) => void
    onLogout?: () => void
    status?: { version: string } | null
    statusError?: string
    currentCategory?: Category | 'miss'
    currentSource?: Source
  } = $props()

  const iconMap: Record<NavKey, typeof Gauge> = {
    overview: Gauge,
    rules: SlidersHorizontal,
    traffic: Activity,
    config: Settings,
  }

  let favoritePages: FavoritePage[] = $state([])

  let favoriteKeySet = $derived(new Set(favoritePages.map((p) => p.key)))
  let favoriteItems = $derived(
    favoritePages
      .map((p) => {
        const item = navItems.find((i) => i.key === p.key)
        return item ? { ...p, item } : null
      })
      .filter((p): p is FavoritePage & { item: NavItem } => p !== null),
  )

  // isFavoritePage matches a pinned page exactly, including its sub-menu
  // state, so 规则 and 规则/block are distinct favorites.
  function isFavoritePage(p: FavoritePage): boolean {
    return favoritePages.some(
      (f) => f.key === p.key && f.category === p.category && f.source === p.source,
    )
  }

  // isCurrentView reports whether a favorite resolves to the view that is
  // active right now (exact sub-menu state), used to highlight shortcuts.
  function isCurrentView(p: FavoritePage): boolean {
    return (
      p.key === activeItem.key &&
      p.category === currentCategory &&
      p.source === currentSource
    )
  }

  // togglePage pins or unpins one exact page (key + sub-menu state).
  function togglePage(page: FavoritePage) {
    if (isFavoritePage(page)) {
      saveFavoritePages(
        favoritePages.filter(
          (f) => !(f.key === page.key && f.category === page.category && f.source === page.source),
        ),
      )
    } else {
      saveFavoritePages([...favoritePages, page])
    }
  }

  // toggleCurrentViewFavorite pins the current view including its sub-menu
  // state, e.g. 规则/block while the rules page shows the block category.
  function toggleCurrentViewFavorite() {
    const page: FavoritePage = { key: activeItem.key }
    if (activeItem.key === 'rules' && currentCategory) page.category = currentCategory
    if (activeItem.key === 'traffic' && currentSource) page.source = currentSource
    togglePage(page)
  }

  // toggleFavoritePage pins/unpins an exact page from a star button; the
  // event is swallowed so the surrounding menu item does not fire.
  function toggleFavoritePage(event: MouseEvent, page: FavoritePage) {
    event.preventDefault()
    event.stopPropagation()
    togglePage(page)
  }

  // favoriteLabel renders a pinned page's label with its sub-menu state,
  // e.g. 规则/block or 流量/http.
  function favoriteLabel(p: FavoritePage): string {
    const item = navItems.find((i) => i.key === p.key)
    const sub = p.category ?? p.source
    return sub ? `${item?.label ?? p.key}/${sub}` : (item?.label ?? p.key)
  }

  // favoritePath renders the deep link for a pinned page, including its
  // sub-menu state: 规则/block -> /rules/block.
  function favoritePath(p: FavoritePage): string {
    if (p.key === 'rules' && p.category) return `/rules/${p.category}`
    if (p.key === 'traffic' && p.source) return `/traffic/${p.source}`
    return p.key === 'overview' ? '/' : `/${p.key}`
  }

  // navigateFavorite handles a left-click on a favorite link in place
  // (SPA); middle-click or modifier clicks fall through to the browser so
  // the link keeps its native semantics (new tab, copy link, drag).
  function navigateFavorite(event: MouseEvent, fav: FavoritePage) {
    if (event.button === 0 && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) {
      event.preventDefault()
      onSelectItem(fav.key, { category: fav.category, source: fav.source })
    }
  }

  const favoriteStorageKey = 'sower.admin.favoritePages.v1'

  onMount(() => {
    favoritePages = loadFavoritePages()
    const handleStorage = (event: StorageEvent) => {
      if (event.key === favoriteStorageKey) {
        favoritePages = loadFavoritePages()
      }
    }
    window.addEventListener('storage', handleStorage)
    return () => window.removeEventListener('storage', handleStorage)
  })

  // loadFavoritePages reads the pinned pages, accepting both the current
  // object format and the legacy string-array format.
  function loadFavoritePages(): FavoritePage[] {
    const knownKeys = new Set(navItems.map((item) => item.key))
    try {
      const parsed = JSON.parse(localStorage.getItem(favoriteStorageKey) ?? '[]')
      if (!Array.isArray(parsed)) return []
      return parsed
        .map((entry): FavoritePage | null => {
          if (typeof entry === 'string') return { key: entry as NavKey } // legacy format
          if (entry && typeof entry === 'object' && typeof entry.key === 'string') {
            return { key: entry.key as NavKey, category: entry.category, source: entry.source }
          }
          return null
        })
        .filter((p): p is FavoritePage => p !== null && knownKeys.has(p.key))
    } catch {
      return []
    }
  }

  function saveFavoritePages(pages: FavoritePage[]) {
    // Dedupe by full identity (key + sub-menu state) so 规则/block and
    // 规则/proxy can be pinned side by side.
    favoritePages = pages.filter(
      (p, index) =>
        pages.findIndex(
          (q) => q.key === p.key && q.category === p.category && q.source === p.source,
        ) === index,
    )
    try {
      localStorage.setItem(favoriteStorageKey, JSON.stringify(favoritePages))
    } catch {
      // localStorage can be unavailable in locked-down browsers; keep in-memory state usable.
    }
  }

  function toggleFavorite(event: MouseEvent, key: NavKey) {
    event.preventDefault()
    event.stopPropagation()
    if (favoriteKeySet.has(key)) {
      // Page-level toggle: unpinning removes every sub-state variant.
      saveFavoritePages(favoritePages.filter((p) => p.key !== key))
      return
    }
    // Record the current sub-menu state so the shortcut lands on the exact
    // view the user pinned.
    const page: FavoritePage = { key }
    if (key === 'rules' && currentCategory) page.category = currentCategory
    if (key === 'traffic' && currentSource) page.source = currentSource
    saveFavoritePages([...favoritePages, page])
  }

  // --- pages menu keyboard navigation (WingGate pattern) ---
  // ArrowUp/Down/Home/End cycle the menu items and their star buttons;
  // opening the menu focuses the active page so keyboard users land where
  // they are.
  let pagesMenuElement: HTMLDivElement | null = $state(null)

  function focusPreferredMenuItem(container: HTMLElement | null, fallbackSelector: string) {
    const item =
      container?.querySelector<HTMLElement>('[aria-current="page"]') ??
      container?.querySelector<HTMLElement>(fallbackSelector)
    item?.focus()
  }

  function handleOpenAutoFocus(event: Event) {
    event.preventDefault()
    tick().then(() => {
      focusPreferredMenuItem(pagesMenuElement, '[data-slot="dropdown-menu-item"]')
    })
  }

  function handlePagesKeydown(event: KeyboardEvent) {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
    const items = Array.from(
      pagesMenuElement?.querySelectorAll<HTMLElement>(
        '[data-slot="dropdown-menu-item"], [data-slot="button"]',
      ) ?? [],
    )
    if (items.length === 0) return
    event.preventDefault()
    const currentIndex = items.indexOf(document.activeElement as HTMLElement)
    const fallbackIndex = event.key === 'End' ? items.length - 1 : 0
    const activeIndex = currentIndex === -1 ? fallbackIndex : currentIndex
    const nextIndex =
      event.key === 'Home'
        ? 0
        : event.key === 'End'
          ? items.length - 1
          : event.key === 'ArrowDown'
            ? (activeIndex + 1) % items.length
            : (activeIndex - 1 + items.length) % items.length
    items[nextIndex]?.focus()
  }

  function toneClass(segment: BreadcrumbContextSegment): string {
    switch (segment.tone) {
      case 'strong':
        return 'text-foreground font-semibold'
      case 'method':
        return 'text-primary font-mono'
      default:
        return 'text-muted-foreground font-medium'
    }
  }
</script>

<header class="sticky top-0 z-20 flex flex-wrap items-center justify-between gap-2 border-b bg-card/95 px-3 py-1.5 backdrop-blur sm:px-4">
  <nav class="flex min-w-0 items-center gap-0.5" aria-label="控制台导航">
    <ol class="flex min-w-0 flex-wrap items-center gap-0.5 text-sm text-muted-foreground">
      <li>
        <Button
          variant="ghost"
          class="h-9 gap-1.5 px-2 text-foreground"
          onclick={() => onSelectItem('overview')}
          title="回到概览"
        >
          <Logo class="size-5" />
          <span class="max-w-[9rem] truncate font-semibold">Sower Admin</span>
        </Button>
      </li>
      <li class="text-muted-foreground/60">/</li>
      <li>
        <DropdownMenu.DropdownMenu>
          {@const ActiveIcon = iconMap[activeItem.key] ?? Gauge}
          <DropdownMenu.DropdownMenuTrigger
            class="inline-flex h-9 max-w-[14rem] items-center gap-1.5 rounded-md px-2 text-sm font-semibold text-foreground transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 aria-expanded:bg-muted"
            aria-label="控制台分区"
          >
            <ActiveIcon class="size-4 shrink-0 text-primary" />
            <span class="truncate">{activeItem.label}</span>
            <ChevronDown class="size-3.5 shrink-0 text-muted-foreground" />
          </DropdownMenu.DropdownMenuTrigger>
          <DropdownMenu.DropdownMenuContent class="min-w-56" align="start">
            {#each navItems as item}
              {@const ItemIcon = iconMap[item.key] ?? Gauge}
              <DropdownMenu.DropdownMenuItem
                onSelect={() => onSelectItem(item.key)}
                class={cn(
                  'gap-2.5 py-2',
                  item.key === activeItem.key && 'bg-accent text-accent-foreground',
                )}
              >
                <span class="inline-flex size-7 items-center justify-center rounded-md border bg-background text-muted-foreground">
                  <ItemIcon class="size-4" />
                </span>
                <span class="flex min-w-0 flex-col">
                  <span class="font-medium">{item.label}</span>
                  <span class="truncate text-xs text-muted-foreground">{item.description}</span>
                </span>
                {#if item.key === activeItem.key}
                  <CircleDot class="ms-auto size-4 text-primary" />
                {/if}
              </DropdownMenu.DropdownMenuItem>
            {/each}
          </DropdownMenu.DropdownMenuContent>
        </DropdownMenu.DropdownMenu>
      </li>

      {#each contextSegments as segment}
        <li class="text-muted-foreground/60 {segment.mobileHidden ? 'hidden sm:list-item' : ''}">/</li>
        <li class="{segment.mobileHidden ? 'hidden sm:list-item' : ''}">
          {#if segment.options?.length}
            <DropdownMenu.DropdownMenu>
              <DropdownMenu.DropdownMenuTrigger
                class={cn(
                  'inline-flex h-9 max-w-[16rem] items-center gap-1.5 rounded-md px-2 text-sm transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 aria-expanded:bg-muted',
                  toneClass(segment),
                )}
                title={segment.title ?? `${segment.label}: ${segment.value}`}
                aria-label={`${segment.label}: ${segment.value}`}
              >
                <span class="hidden text-xs font-normal text-muted-foreground md:inline">{segment.label}</span>
                <span class="truncate">{segment.value}</span>
                <ChevronDown class="size-3.5 shrink-0 text-muted-foreground" />
              </DropdownMenu.DropdownMenuTrigger>
              <DropdownMenu.DropdownMenuContent class="min-w-44" align="start">
                {#each segment.options as option}
                  {@const fav: FavoritePage = {
                    key: activeItem.key,
                    category: segment.label === '分类' ? (option.value as Category | 'miss') : undefined,
                    source: segment.label === 'source' ? (option.value as Source) : undefined,
                  }}
                  {@const pinned = isFavoritePage(fav)}
                  <div
                    class={cn(
                      'flex items-center gap-0.5 rounded-md',
                      option.active && 'bg-accent text-accent-foreground',
                    )}
                  >
                    <DropdownMenu.DropdownMenuItem
                      onSelect={() => onContextSelect(segment.label, option.value)}
                      class="min-w-0 flex-1 gap-2"
                    >
                      <span class="font-medium">{option.label}</span>
                      {#if option.description}
                        <span class="text-xs text-muted-foreground">{option.description}</span>
                      {/if}
                      {#if option.active}
                        <CircleDot class="ms-auto size-4 text-primary" />
                      {/if}
                    </DropdownMenu.DropdownMenuItem>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      class={cn(
                        'mx-0.5 shrink-0 rounded-full hover:bg-primary/10 hover:text-primary',
                        pinned && 'bg-primary/10 text-primary',
                      )}
                      aria-label={`${pinned ? '取消收藏' : '收藏'} ${option.label}`}
                      aria-pressed={pinned}
                      onclick={(event) => toggleFavoritePage(event, fav)}
                    >
                      <Star class={cn('size-3.5', pinned && 'fill-current')} />
                    </Button>
                  </div>
                {/each}
                <DropdownMenu.DropdownMenuSeparator />
                {@const currentFav: FavoritePage = {
                  key: activeItem.key,
                  category: segment.label === '分类' ? (segment.value as Category | 'miss') : undefined,
                  source: segment.label === 'source' ? (segment.value as Source) : undefined,
                }}
                {@const pinned = isFavoritePage(currentFav)}
                <DropdownMenu.DropdownMenuItem onSelect={toggleCurrentViewFavorite} class="gap-2">
                  <Star class={cn('size-4', pinned && 'fill-current text-primary')} />
                  <span>{pinned ? '已收藏当前视图' : '收藏当前视图'}</span>
                </DropdownMenu.DropdownMenuItem>
              </DropdownMenu.DropdownMenuContent>
            </DropdownMenu.DropdownMenu>
          {:else}
            <span
              class={cn('inline-flex h-9 max-w-[16rem] items-center gap-1.5 px-2', toneClass(segment))}
              title={segment.title ?? `${segment.label}: ${segment.value}`}
            >
              <span class="hidden text-xs font-normal text-muted-foreground md:inline">{segment.label}</span>
              <span class="truncate">{segment.value}</span>
            </span>
          {/if}
        </li>
      {/each}
    </ol>
  </nav>

  <div class="flex shrink-0 items-center gap-1.5">
    {#if favoriteItems.length > 0}
      <div class="hidden items-center gap-0.5 sm:flex" aria-label="收藏页面">
        {#each favoriteItems.slice(0, 3) as fav}
          {@const active = isCurrentView(fav)}
          <a
            href={favoritePath(fav)}
            class={cn(
              'inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-sm font-medium transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50',
              active && 'bg-primary/10 text-primary',
            )}
            title={`打开 ${favoriteLabel(fav)}`}
            aria-current={active ? 'page' : undefined}
            onclick={(e) => navigateFavorite(e, fav)}
          >
            <Star
              class={cn(
                'size-3.5 shrink-0',
                active ? 'fill-current text-primary' : 'text-muted-foreground',
              )}
            />
            <span class="hidden max-w-[5rem] truncate lg:inline">{favoriteLabel(fav)}</span>
          </a>
        {/each}
      </div>
    {/if}

    <DropdownMenu.DropdownMenu>
      <DropdownMenu.DropdownMenuTrigger
        class="inline-flex h-9 items-center gap-1.5 rounded-md border border-input bg-background px-2.5 text-sm font-medium text-foreground transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 aria-expanded:bg-muted sm:h-7"
        aria-label="页面与收藏夹"
      >
        <LayoutPanelTop class="size-3.5" />
        <span class="hidden sm:inline">页面</span>
        <ChevronDown class="size-3 text-muted-foreground" />
      </DropdownMenu.DropdownMenuTrigger>
      <DropdownMenu.DropdownMenuContent
        class="w-64"
        bind:ref={pagesMenuElement}
        onOpenAutoFocus={handleOpenAutoFocus}
        onkeydown={handlePagesKeydown}
      >
        <DropdownMenu.DropdownMenuLabel>收藏夹</DropdownMenu.DropdownMenuLabel>
        {#if favoriteItems.length > 0}
          <div class="grid gap-0.5 pb-1">
            {#each favoriteItems as fav}
              {@const ItemIcon = iconMap[fav.key] ?? Gauge}
              {@const active = isCurrentView(fav)}
              <div
                class={cn(
                  'flex items-center gap-0.5 rounded-md',
                  active && 'bg-accent text-accent-foreground',
                )}
              >
                <DropdownMenu.DropdownMenuItem
                  onSelect={() => onSelectItem(fav.key, { category: fav.category, source: fav.source })}
                  class="min-w-0 flex-1 gap-2 py-1.5"
                  title={favoritePath(fav)}
                >
                  <span class="inline-flex size-6 shrink-0 items-center justify-center rounded-md border border-primary/25 bg-primary/10 text-primary">
                    <ItemIcon class="size-3.5" />
                  </span>
                  <span class="truncate font-medium">{favoriteLabel(fav)}</span>
                </DropdownMenu.DropdownMenuItem>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  class="mx-0.5 shrink-0 rounded-full text-primary hover:bg-primary/10 hover:text-primary"
                  aria-label={`取消收藏 ${favoriteLabel(fav)}`}
                  onclick={(event) => toggleFavoritePage(event, fav)}
                >
                  <Star class="size-3.5 fill-current" />
                </Button>
              </div>
            {/each}
          </div>
        {:else}
          <p class="mx-2 mb-1 rounded-md bg-muted px-2.5 py-2 text-xs leading-5 text-muted-foreground">
            暂无收藏。点击下方星标加入常用入口。
          </p>
        {/if}

        <DropdownMenu.DropdownMenuSeparator />

        <DropdownMenu.DropdownMenuLabel>全部页面</DropdownMenu.DropdownMenuLabel>
        <div class="grid gap-0.5 pb-1">
          {#each navItems as item}
            {@const ItemIcon = iconMap[item.key] ?? Gauge}
            {@const pinned = favoriteKeySet.has(item.key)}
            <div
              class={cn(
                'flex items-center gap-0.5 rounded-md',
                item.key === activeItem.key && 'bg-accent text-accent-foreground',
              )}
            >
              <DropdownMenu.DropdownMenuItem
                onSelect={() => onSelectItem(item.key)}
                class="min-w-0 flex-1 gap-2 py-1.5"
              >
                <span class="inline-flex size-7 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground">
                  <ItemIcon class="size-4" />
                </span>
                <span class="flex min-w-0 flex-col">
                  <span class="font-medium">{item.label}</span>
                  <span class="truncate text-xs text-muted-foreground">{item.description}</span>
                </span>
              </DropdownMenu.DropdownMenuItem>
              <Button
                variant="ghost"
                size="icon-sm"
                class={cn(
                  'mx-0.5 shrink-0 rounded-full hover:bg-primary/10 hover:text-primary',
                  pinned && 'bg-primary/10 text-primary',
                )}
                aria-label={`${pinned ? '取消收藏' : '收藏'} ${item.label}`}
                aria-pressed={pinned}
                onclick={(event) => toggleFavorite(event, item.key)}
              >
                <Star class={cn('size-4', pinned && 'fill-current')} />
              </Button>
            </div>
          {/each}
        </div>
      </DropdownMenu.DropdownMenuContent>
    </DropdownMenu.DropdownMenu>

    {#if statusError}
      <Badge variant="destructive" class="hidden sm:inline-flex">
        错误
      </Badge>
    {/if}

    <Button variant="ghost" size="icon-sm" class="size-9 sm:size-7" title="退出登录" aria-label="退出登录" onclick={onLogout}>
      <LogOut class="size-4" />
    </Button>
  </div>
</header>
