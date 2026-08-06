// live.svelte.ts — SSE-fed real-time state shared by the admin pages.
//
// One EventSource connection streams status/traffic/history events from
// /api/stream; the browser auto-reconnects on drops. The connection carries
// the active page's sort/source/client filters, so switching a filter
// re-points the stream (connectLive reconnects only when params change).
import type { HistorySample, Status, TrafficSnapshot } from '$lib/api'

export const live = $state({
	status: null as Status | null,
	traffic: null as TrafficSnapshot | null,
	history: [] as HistorySample[],
	connected: false,
})

let es: EventSource | null = null
let activeParams = ''
let unauthorizedHandler: (() => void) | null = null

// safeParse tolerates a malformed SSE payload without breaking the stream
// handler; the previous value stays in place until a valid event arrives.
function safeParse<T>(data: string): T | null {
	try {
		return JSON.parse(data) as T
	} catch {
		return null
	}
}

export function setUnauthorizedHandler(fn: (() => void) | null) {
	unauthorizedHandler = fn
}

// connectLive opens the SSE stream, or re-points it when the filter params
// change. Calling it with the current params is a no-op.
export function connectLive(params = '') {
	if (es && params === activeParams) return
	activeParams = params
	es?.close()
	es = new EventSource(`/api/stream${params ? `?${params}` : ''}`)
	es.onopen = () => (live.connected = true)
	es.onerror = () => (live.connected = false)
	es.addEventListener('status', (e) => {
		const parsed = safeParse<Status>(e.data)
		if (parsed) live.status = parsed
	})
	es.addEventListener('traffic', (e) => {
		const parsed = safeParse<TrafficSnapshot>(e.data)
		if (parsed) live.traffic = parsed
	})
	es.addEventListener('history', (e) => {
		const parsed = safeParse<{ samples: HistorySample[] }>(e.data)
		if (parsed) live.history = parsed.samples
	})
	es.addEventListener('auth', () => {
		unauthorizedHandler?.()
	})
}

export function closeLive() {
	es?.close()
	es = null
	activeParams = ''
	live.status = null
	live.traffic = null
	live.history = []
	live.connected = false
}
