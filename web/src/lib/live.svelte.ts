// live.svelte.ts — SSE-fed real-time state shared by the admin pages.
//
// One EventSource connection streams status/traffic/history events from
// /api/stream; the browser auto-reconnects on drops. The connection carries
// the active page's sort/source/client filters, so switching a filter
// re-points the stream (connectLive reconnects only when params change).
import {
	ApiError,
	probeSession,
	type HistorySample,
	type Status,
	type Totals,
	type TrafficSnapshot,
} from "$lib/api";

export const live = $state({
	status: null as Status | null,
	traffic: null as TrafficSnapshot | null,
	history: [] as HistorySample[],
	totals: null as Totals | null,
	connected: false,
});

let es: EventSource | null = null;
let activeParams = "";
let unauthorizedHandler: (() => void) | null = null;
let generation = 0;

// safeParse tolerates a malformed SSE payload without breaking the stream
// handler; the previous value stays in place until a valid event arrives.
function safeParse<T>(data: string): T | null {
	try {
		return JSON.parse(data) as T;
	} catch {
		return null;
	}
}

function isCurrent(source: EventSource, sourceGeneration: number): boolean {
	return es === source && generation === sourceGeneration;
}

function reconnectAfterRenewal(source: EventSource, sourceGeneration: number) {
	if (!isCurrent(source, sourceGeneration)) return;

	const params = activeParams;
	es = null;
	generation++;
	const renewalGeneration = generation;
	source.close();
	live.connected = false;

	void probeSession()
		.then(() => {
			if (generation === renewalGeneration && es === null) connectLive(params);
		})
		.catch((error) => {
			if (generation !== renewalGeneration || es !== null) return;
			if (error instanceof ApiError && error.status === 401) {
				unauthorizedHandler?.();
				return;
			}
			// Keep the native reconnection model even when the renewal probe itself
			// failed due to a transient network problem.
			connectLive(params);
		});
}

export function setUnauthorizedHandler(fn: (() => void) | null) {
	unauthorizedHandler = fn;
}

// connectLive opens the SSE stream, or re-points it when the filter params
// change. Calling it with the current params is a no-op.
export function connectLive(params = "") {
	if (es && params === activeParams) return;

	const previous = es;
	es = null;
	previous?.close();
	activeParams = params;

	const source = new EventSource(`/api/stream${params ? `?${params}` : ""}`);
	const sourceGeneration = ++generation;
	let checkingSession = false;
	es = source;

	source.onopen = () => {
		if (!isCurrent(source, sourceGeneration)) return;
		checkingSession = false;
		live.connected = true;
	};
	source.onerror = () => {
		if (!isCurrent(source, sourceGeneration)) return;
		live.connected = false;
		if (checkingSession) return;
		checkingSession = true;
		void probeSession().catch((error) => {
			if (!isCurrent(source, sourceGeneration)) return;
			if (error instanceof ApiError && error.status === 401) unauthorizedHandler?.();
		});
	};
	source.addEventListener("status", (e) => {
		if (!isCurrent(source, sourceGeneration)) return;
		const parsed = safeParse<Status>(e.data);
		if (parsed) live.status = parsed;
	});
	source.addEventListener("traffic", (e) => {
		if (!isCurrent(source, sourceGeneration)) return;
		const parsed = safeParse<TrafficSnapshot>(e.data);
		if (parsed) live.traffic = parsed;
	});
	source.addEventListener("history", (e) => {
		if (!isCurrent(source, sourceGeneration)) return;
		const parsed = safeParse<{ samples: HistorySample[] }>(e.data);
		if (parsed) live.history = parsed.samples;
	});
	source.addEventListener("totals", (e) => {
		if (!isCurrent(source, sourceGeneration)) return;
		const parsed = safeParse<Totals>(e.data);
		if (parsed) live.totals = parsed;
	});
	source.addEventListener("renew", () => {
		reconnectAfterRenewal(source, sourceGeneration);
	});
	source.addEventListener("auth", () => {
		if (!isCurrent(source, sourceGeneration)) return;
		unauthorizedHandler?.();
	});
}

export function closeLive() {
	const source = es;
	es = null;
	generation++;
	source?.close();
	activeParams = "";
	live.status = null;
	live.traffic = null;
	live.history = [];
	live.totals = null;
	live.connected = false;
}
