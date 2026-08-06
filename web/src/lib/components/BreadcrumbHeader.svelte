<script lang="ts">
  import { onMount } from 'svelte'
  import { Activity, ChevronDown, CircleDot, Gauge, LayoutPanelTop, LogOut, SlidersHorizontal, Star } from 'lucide-svelte'
  import * as DropdownMenu from '$lib/components/ui/dropdown-menu'
  import Badge from '$lib/components/ui/badge/badge.svelte'
  import Button from '$lib/components/ui/button/button.svelte'
  import { navItems, type BreadcrumbContextSegment, type NavItem, type NavKey } from '../navigation'
  import { cn } from '$lib/utils'

  let {
    activeItem,
    contextSegments = [],
    onContextSelect = () => {},
    onSelectItem = () => {},
    onLogout = () => {},
    status = null,
    statusError = '',
  }: {
    activeItem: NavItem
    contextSegments?: BreadcrumbContextSegment[]
    onContextSelect?: (label: string, value: string) => void
    onSelectItem?: (key: NavKey) => void
    onLogout?: () => void
    status?: { version: string } | null
    statusError?: string
  } = $props()

  const iconMap: Record<NavKey, typeof Gauge> = {
    overview: Gauge,
    rules: SlidersHorizontal,
    traffic: Activity,
  }

  let favoriteKeys: NavKey[] = $state([])

  let favoriteKeySet = $derived(new Set(favoriteKeys))
  let favoriteItems = $derived(
    favoriteKeys
      .map((key) => navItems.find((item) => item.key === key))
      .filter((item): item is NavItem => Boolean(item)),
  )

  const favoriteStorageKey = 'sower.admin.favoritePages.v1'

  onMount(() => {
    favoriteKeys = loadFavoriteKeys()
    const handleStorage = (event: StorageEvent) => {
      if (event.key === favoriteStorageKey) {
        favoriteKeys = loadFavoriteKeys()
      }
    }
    window.addEventListener('storage', handleStorage)
    return () => window.removeEventListener('storage', handleStorage)
  })

  function loadFavoriteKeys(): NavKey[] {
    const knownKeys = new Set(navItems.map((item) => item.key))
    try {
      const parsed = JSON.parse(localStorage.getItem(favoriteStorageKey) ?? '[]')
      return (Array.isArray(parsed) ? parsed : []).filter((key): key is NavKey => knownKeys.has(key))
    } catch {
      return []
    }
  }

  function saveFavoriteKeys(keys: NavKey[]) {
    favoriteKeys = keys.filter((key, index) => keys.indexOf(key) === index)
    try {
      localStorage.setItem(favoriteStorageKey, JSON.stringify(favoriteKeys))
    } catch {
      // localStorage can be unavailable in locked-down browsers; keep in-memory state usable.
    }
  }

  function toggleFavorite(event: MouseEvent, key: NavKey) {
    event.preventDefault()
    event.stopPropagation()
    if (favoriteKeySet.has(key)) {
      saveFavoriteKeys(favoriteKeys.filter((item) => item !== key))
      return
    }
    saveFavoriteKeys([...favoriteKeys, key])
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
  <nav class="flex min-w-0 items-center gap-0.5" aria-label="Console breadcrumb">
    <ol class="flex min-w-0 flex-wrap items-center gap-0.5 text-sm text-muted-foreground">
      <li>
        <Button
          variant="ghost"
          class="h-9 gap-1.5 px-2 text-foreground"
          onclick={() => onSelectItem('overview')}
          title="回到概览"
        >
          <Gauge class="size-4 text-primary" />
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
        <li class="text-muted-foreground/60">/</li>
        <li>
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
                  <DropdownMenu.DropdownMenuItem
                    onSelect={() => onContextSelect(segment.label, option.value)}
                    class={cn('gap-2', option.active && 'bg-accent text-accent-foreground')}
                  >
                    <span class="font-medium">{option.label}</span>
                    {#if option.description}
                      <span class="text-xs text-muted-foreground">{option.description}</span>
                    {/if}
                    {#if option.active}
                      <CircleDot class="ms-auto size-4 text-primary" />
                    {/if}
                  </DropdownMenu.DropdownMenuItem>
                {/each}
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
      <div class="hidden items-center gap-1 md:flex" aria-label="Favorite pages">
        {#each favoriteItems.slice(0, 4) as item}
          {@const ItemIcon = iconMap[item.key] ?? Gauge}
          <Button
            variant="outline"
            size="sm"
            class={cn('gap-1.5', item.key === activeItem.key && 'border-primary/40 bg-primary/10')}
            title={`打开 ${item.label}`}
            onclick={() => onSelectItem(item.key)}
          >
            <ItemIcon class="size-3.5" />
            <span class="max-w-[5rem] truncate">{item.label}</span>
          </Button>
        {/each}
      </div>
    {/if}

    <DropdownMenu.DropdownMenu>
      <DropdownMenu.DropdownMenuTrigger
        class="inline-flex h-7 items-center gap-1.5 rounded-md border border-input bg-background px-2.5 text-sm font-medium text-foreground transition-colors outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 aria-expanded:bg-muted"
        aria-label="页面与收藏夹"
      >
        <LayoutPanelTop class="size-3.5" />
        <span class="hidden sm:inline">页面</span>
        <ChevronDown class="size-3 text-muted-foreground" />
      </DropdownMenu.DropdownMenuTrigger>
      <DropdownMenu.DropdownMenuContent class="w-64">
        <DropdownMenu.DropdownMenuLabel>收藏夹</DropdownMenu.DropdownMenuLabel>
        {#if favoriteItems.length > 0}
          <div class="grid gap-0.5 pb-1">
            {#each favoriteItems as item}
              {@const ItemIcon = iconMap[item.key] ?? Gauge}
              <DropdownMenu.DropdownMenuItem
                onSelect={() => onSelectItem(item.key)}
                class={cn('gap-2 py-1.5', item.key === activeItem.key && 'bg-accent text-accent-foreground')}
              >
                <span class="inline-flex size-6 items-center justify-center rounded-md border border-primary/25 bg-primary/10 text-primary">
                  <ItemIcon class="size-3.5" />
                </span>
                <span class="font-medium">{item.label}</span>
                <Star class="ms-auto size-3.5 fill-current text-primary" />
              </DropdownMenu.DropdownMenuItem>
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
                class={cn('mx-0.5 shrink-0', pinned && 'text-primary')}
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

    <Badge
      variant={statusError ? 'destructive' : status ? 'secondary' : 'outline'}
      class="hidden sm:inline-flex"
    >
      {statusError ? 'Error' : status ? 'Ready' : 'Loading'}
    </Badge>

    <Button variant="ghost" size="icon-sm" title="退出登录" aria-label="退出登录" onclick={onLogout}>
      <LogOut class="size-4" />
    </Button>
  </div>
</header>
