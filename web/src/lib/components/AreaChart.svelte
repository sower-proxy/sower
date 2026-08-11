<script lang="ts">
  import { onDestroy } from 'svelte'
  import { chartLink } from '$lib/chart-link.svelte.ts'

  export interface ChartSeries {
    name: string
    color: string
    values: number[]
  }

  let {
    series,
    labels,
    timestamps,
    height = 160,
    label = '历史数据曲线',
    format = (v: number) => String(v),
  }: {
    series: ChartSeries[]
    labels: string[]
    // timestamps carries the full ISO instant per sample. Charts link on it:
    // hovering one chart shows the crosshair and values at the same instant
    // in every other chart sharing the same timeline.
    timestamps: string[]
    height?: number
    // label is the accessible name of the chart, distinct per usage site so
    // screen-reader users can tell the charts apart.
    label?: string
    format?: (v: number) => string
  } = $props()

  const W = 600
  const H = $derived(height)

  const all = $derived(series.flatMap((s) => s.values))
  const max = $derived(all.length ? Math.max(...all) : 0)
  const min = $derived(all.length ? Math.min(...all) : 0)
  const range = $derived(max - min || 1)
  const pad = $derived(range * 0.12)
  const top = $derived(max + pad)
  const bottom = $derived(Math.max(0, min - pad))

  const x = $derived((i: number) => (labels.length <= 1 ? 0 : (i / (labels.length - 1)) * W))
  const y = $derived((v: number) => H - ((v - bottom) / (top - bottom)) * H)

  // Cached SVG path data per series: the area path reuses the line path, so
  // computing them together avoids walking every sample twice per frame.
  const charts = $derived(
    series.map((s) => {
      const values = s.values
      if (!values.length) return { s, line: '', area: '' }
      const linePath = values
        .map((v, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(v).toFixed(1)}`)
        .join(' ')
      return {
        s,
        line: linePath,
        area: `${linePath} L${x(values.length - 1).toFixed(1)},${H} L${x(0).toFixed(1)},${H} Z`,
      }
    }),
  )

  // Timestamp lookups happen on every linked hover; a Map turns the repeated
  // linear indexOf scans into constant-time lookups.
  const indexByTimestamp = $derived(new Map(timestamps.map((t, i) => [t, i])))

  const axisPos = $derived(
    labels.length <= 2
      ? [0, labels.length - 1]
      : [0, Math.floor(labels.length / 2), labels.length - 1],
  )
  const axisLabels = $derived(axisPos.map((p) => labels[p]))
  const hasData = $derived(labels.length >= 2)

  // hoverTimestamp is this chart's own pointer position; the linked
  // timestamp comes from another chart. Timestamps survive history-window
  // shifts whereas a raw index would point at a different sample.
  const chartOwner = Symbol('area-chart')
  let hoverTimestamp = $state<string | null>(null)
  const ownIndex = $derived(
    hoverTimestamp == null ? null : indexByTimestamp.get(hoverTimestamp) ?? null,
  )
  const linkedIndex = $derived(
    chartLink.timestamp == null ? null : indexByTimestamp.get(chartLink.timestamp) ?? null,
  )
  const shown = $derived(
    ownIndex != null && ownIndex >= 0
      ? ownIndex
      : linkedIndex != null && linkedIndex >= 0
        ? linkedIndex
        : null,
  )
  const shownTime = $derived(
    shown != null && timestamps[shown]
      ? new Date(timestamps[shown]).toTimeString().slice(0, 8)
      : '',
  )

  let svg = $state<SVGSVGElement>()
  let rafId: number | null = null
  let pendingIndex: number | null = null
  // Touch has no persistent hover: a tap pins the crosshair so the value
  // readout stays visible after the finger lifts; a second tap releases it.
  // The pin commits on pointerup so scroll gestures (which end in
  // pointercancel) never leave a stray crosshair behind.
  let pinned = $state(false)
  let tapStart = $state<{ x: number; y: number } | null>(null)

  // The sr-only status announces the point this chart itself is inspecting
  // (pointer, keyboard, or a pin). Charts that merely mirror another chart's
  // crosshair via chartLink stay silent, so a linked hover never produces a
  // burst of announcements from every chart on the page. The region stays in
  // the DOM permanently; screen readers announce content changes reliably,
  // which is not guaranteed for regions inserted and removed on hover.
  const statusText = $derived(
    shown != null && chartLink.owner === chartOwner
      ? `${shownTime}：${series
          .map((s) => {
            const v = s.values[shown] ?? null
            return `${s.name} ${v == null ? '-' : format(v)}`
          })
          .join(', ')}`
      : '',
  )

  function clearOwnedLink() {
    if (chartLink.owner !== chartOwner) return
    chartLink.timestamp = null
    chartLink.owner = null
  }

  // Pointer events can fire faster than frames; apply the latest position
  // once per animation frame instead of updating state on every move.
  function applyHover() {
    rafId = null
    if (pendingIndex == null) return
    const timestamp = timestamps[pendingIndex] ?? null
    hoverTimestamp = timestamp
    chartLink.timestamp = timestamp
    chartLink.owner = timestamp == null ? null : chartOwner
  }

  function onMove(e: PointerEvent) {
    if (!svg) return
    const rect = svg.getBoundingClientRect()
    if (rect.width === 0 || labels.length < 2) return
    const frac = (e.clientX - rect.left) / rect.width
    const i = Math.max(0, Math.min(labels.length - 1, Math.round(frac * (labels.length - 1))))
    pendingIndex = i
    if (rafId != null) return
    rafId = requestAnimationFrame(applyHover)
  }

  // The chart is focusable so keyboard users can inspect samples with the
  // arrow keys, matching what pointer hover reveals. shown is the starting
  // point: it may be a linked crosshair from another chart.
  function onKeyDown(e: KeyboardEvent) {
    if (!hasData || labels.length < 2) return
    if (e.key === 'Escape') {
      // Escape only clears crosshairs this chart owns; a crosshair linked
      // from another chart stays until its source chart releases it.
      pinned = false
      cancelPending()
      hoverTimestamp = null
      clearOwnedLink()
      return
    }
    const base = shown ?? 0
    let next: number | null = null
    switch (e.key) {
      case 'ArrowLeft':
        next = Math.max(0, base - 1)
        break
      case 'ArrowRight':
        next = Math.min(labels.length - 1, base + 1)
        break
      case 'Home':
        next = 0
        break
      case 'End':
        next = labels.length - 1
        break
      default:
        return
    }
    e.preventDefault()
    pendingIndex = next
    applyHover()
  }

  function cancelPending() {
    if (rafId != null) {
      cancelAnimationFrame(rafId)
      rafId = null
    }
    pendingIndex = null
  }

  function onLeave() {
    if (pinned) return
    cancelPending()
    hoverTimestamp = null
    clearOwnedLink()
  }

  // A pinned timestamp can slide out of the history window on the next SSE
  // push; release the stale pin so the legend falls back to the live value
  // and the next tap starts fresh.
  $effect(() => {
    if (pinned && shown === null) {
      pinned = false
      hoverTimestamp = null
      clearOwnedLink()
    }
  })

  function onPointerDown(e: PointerEvent) {
    if (e.pointerType !== 'touch') return
    tapStart = { x: e.clientX, y: e.clientY }
  }

  function onPointerUp(e: PointerEvent) {
    if (e.pointerType !== 'touch' || tapStart == null) return
    const dx = e.clientX - tapStart.x
    const dy = e.clientY - tapStart.y
    tapStart = null
    // Only a tap (not a drag) toggles the pin; dragging just moves the
    // crosshair via pointermove while the finger is down.
    if (Math.hypot(dx, dy) > 8) return
    if (pinned) {
      pinned = false
      cancelPending()
      hoverTimestamp = null
      clearOwnedLink()
      return
    }
    pinned = true
    // Position the crosshair at the tap point synchronously so the pin is
    // visible as soon as the finger lifts, not one frame later.
    if (!svg) return
    const rect = svg.getBoundingClientRect()
    if (rect.width === 0 || labels.length < 2) return
    const frac = (e.clientX - rect.left) / rect.width
    const i = Math.max(0, Math.min(labels.length - 1, Math.round(frac * (labels.length - 1))))
    pendingIndex = i
    applyHover()
  }

  // A scroll gesture from the chart area fires pointercancel; roll back the
  // transient hover so no stray crosshair survives a scroll, and drop any
  // pending tap.
  function onPointerCancel(e: PointerEvent) {
    if (e.pointerType !== 'touch') return
    tapStart = null
    if (pinned) return
    cancelPending()
    hoverTimestamp = null
    clearOwnedLink()
  }

  onDestroy(() => {
    cancelPending()
    clearOwnedLink()
  })
</script>

<div class="relative">
  {#if hasData}
    <div class="relative">
      <svg
        bind:this={svg}
        viewBox={`0 0 ${W} ${H}`}
        preserveAspectRatio="none"
        class="w-full cursor-crosshair focus-visible:outline-2 focus-visible:outline-ring focus-visible:outline-offset-2"
        style={`height:${H}px`}
        role="img"
        aria-label={label}
        tabindex="0"
        onpointermove={onMove}
        onpointerdown={onPointerDown}
        onpointerup={onPointerUp}
        onpointercancel={onPointerCancel}
        onpointerleave={onLeave}
        onkeydown={onKeyDown}
      >
        {#each [0.25, 0.5, 0.75] as g}
          <line
            x1="0"
            x2={W}
            y1={H * g}
            y2={H * g}
            class="stroke-border/60"
            stroke-width="1"
            vector-effect="non-scaling-stroke"
          />
        {/each}
        {#each charts as c}
          <path d={c.area} style={`fill:${c.s.color}`} opacity="0.1" />
        {/each}
        {#each charts as c}
          <path
            d={c.line}
            fill="none"
            style={`stroke:${c.s.color}`}
            stroke-width="1.5"
            vector-effect="non-scaling-stroke"
            stroke-linejoin="round"
            stroke-linecap="round"
          />
        {/each}
        {#if shown !== null}
          <line
            x1={x(shown)}
            x2={x(shown)}
            y1="0"
            y2={H}
            class="stroke-foreground/25"
            stroke-width="1"
            vector-effect="non-scaling-stroke"
          />
        {/if}
      </svg>
      <span class="sr-only" role="status">{statusText}</span>
      {#if shown !== null}
        <!-- Markers and the timestamp badge live outside the SVG: the viewBox
             stretches with preserveAspectRatio="none", which would distort
             any SVG circle or text. -->
        {#each series as s}
          <span
            class="pointer-events-none absolute size-2 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-background"
            style={`left:${(x(shown) / W) * 100}%;top:${(y(s.values[shown] ?? 0) / H) * 100}%;background:${s.color}`}
          ></span>
        {/each}
        <span
          class="pointer-events-none absolute top-0 -translate-x-1/2 rounded border bg-popover px-1.5 py-0.5 text-xs tabular-nums text-muted-foreground shadow-sm"
          style={`left:${Math.min(92, Math.max(8, (x(shown) / W) * 100))}%`}
        >
          {shownTime}
        </span>
      {/if}
    </div>

    <div class="mt-1.5 flex items-center justify-between text-xs text-muted-foreground">
      {#each axisLabels as l}
        <span>{l}</span>
      {/each}
    </div>
  {:else}
    <div
      class="flex items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground"
      style={`height:${H}px`}
    >
      等待采样数据…
    </div>
  {/if}

  <!-- The legend always shows a live value: the crosshair point while one is
       active, otherwise the latest sample, so the current level is readable
       without hovering (and on touch devices at all). With fewer than two
       samples there is no rate yet; the chart body shows the waiting state
       and the legend omits values instead of fabricating a zero. -->
  <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
    {#each series as s}
      {@const valueIndex = shown ?? (s.values.length > 1 ? s.values.length - 1 : null)}
      {@const value = valueIndex != null ? (s.values[valueIndex] ?? null) : null}
      <span class="inline-flex items-center gap-1.5">
        <span class="size-2 rounded-full" style={`background:${s.color}`}></span>
        {s.name}
        {#if value != null}
          <span class="tabular-nums text-foreground">{format(value)}</span>
        {/if}
      </span>
    {/each}
  </div>
</div>
