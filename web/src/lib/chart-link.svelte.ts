// chart-link.svelte.ts — shared hover state that synchronizes every
// AreaChart on the time axis. The link key is the full ISO timestamp, never
// the HH:MM axis label, which can collide across days and shifts as the
// history window slides. owner prevents an old chart's pointerleave from
// clearing a newer hover published by another chart.
export const chartLink = $state<{ timestamp: string | null; owner: symbol | null }>({
  timestamp: null,
  owner: null,
});
