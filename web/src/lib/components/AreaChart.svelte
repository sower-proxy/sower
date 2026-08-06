<script lang="ts">
  export interface ChartSeries {
    name: string
    color: string
    values: number[]
  }

  let {
    series,
    labels,
    height = 160,
    format = (v: number) => String(v),
  }: {
    series: ChartSeries[]
    labels: string[]
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

  const line = $derived((values: number[]) => {
    if (!values.length) return ''
    return values
      .map((v, i) => `${i === 0 ? 'M' : 'L'}${x(i).toFixed(1)},${y(v).toFixed(1)}`)
      .join(' ')
  })
  const area = $derived((values: number[]) => {
    if (!values.length) return ''
    return `${line(values)} L${x(values.length - 1).toFixed(1)},${H} L${x(0).toFixed(1)},${H} Z`
  })

  const axisPos = $derived(
    labels.length <= 2
      ? [0, labels.length - 1]
      : [0, Math.floor(labels.length / 2), labels.length - 1],
  )
  const axisLabels = $derived(axisPos.map((p) => labels[p]))
  const hasData = $derived(labels.length >= 2)

  let hover = $state<number | null>(null)
  let svg = $state<SVGSVGElement>()

  function onMove(e: MouseEvent) {
    if (!svg) return
    const rect = svg.getBoundingClientRect()
    if (rect.width === 0 || labels.length < 2) return
    const frac = (e.clientX - rect.left) / rect.width
    hover = Math.max(0, Math.min(labels.length - 1, Math.round(frac * (labels.length - 1))))
  }
</script>

<div class="relative">
  {#if hasData}
    <svg
      bind:this={svg}
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      class="w-full cursor-crosshair"
      style={`height:${H}px`}
      role="img"
      aria-label="历史数据曲线"
      onmousemove={onMove}
      onmouseleave={() => (hover = null)}
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
      {#each series as s}
        <path d={area(s.values)} style={`fill:${s.color}`} opacity="0.1" />
      {/each}
      {#each series as s}
        <path
          d={line(s.values)}
          fill="none"
          style={`stroke:${s.color}`}
          stroke-width="1.5"
          vector-effect="non-scaling-stroke"
          stroke-linejoin="round"
          stroke-linecap="round"
        />
      {/each}
      {#if hover !== null}
        <line
          x1={x(hover)}
          x2={x(hover)}
          y1="0"
          y2={H}
          class="stroke-foreground/25"
          stroke-width="1"
          vector-effect="non-scaling-stroke"
        />
      {/if}
    </svg>

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
        {#if hover !== null}
          <span class="tabular-nums text-foreground">{format(s.values[hover] ?? 0)}</span>
        {/if}
      </span>
    {/each}
  </div>
</div>
