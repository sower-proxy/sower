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
    format = (v: number) => String(v),
  }: {
    series: ChartSeries[]
    labels: string[]
    // timestamps carries the full ISO instant per sample. Charts link on it:
    // hovering one chart shows the crosshair and values at the same instant
    // in every other chart sharing the same timeline.
    timestamps: string[]
    height?: number
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

  function cancelPending() {
    if (rafId != null) {
      cancelAnimationFrame(rafId)
      rafId = null
    }
    pendingIndex = null
  }

  function onLeave() {
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
        class="w-full cursor-crosshair"
        style={`height:${H}px`}
        role="img"
        aria-label="历史数据曲线"
        onpointermove={onMove}
        onpointerleave={onLeave}
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
          class="pointer-events-none absolute top-0 -translate-x-1/2 rounded border bg-popover px-1.5 py-0.5 text-[10px] tabular-nums text-muted-foreground shadow-sm"
          style={`left:${Math.min(92, Math.max(8, (x(shown) / W) * 100))}%`}
        >
          {shownTime}
        </span>
      {/if}
    </div>

    <div class="mt-1.5 flex items-center justify-between text-[10px] text-muted-foreground">
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

  <div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
    {#each series as s}
      <span class="inline-flex items-center gap-1.5">
        <span class="size-2 rounded-full" style={`background:${s.color}`}></span>
        {s.name}
        {#if shown !== null}
          <span class="tabular-nums text-foreground">{format(s.values[shown] ?? 0)}</span>
        {/if}
      </span>
    {/each}
  </div>
</div>
