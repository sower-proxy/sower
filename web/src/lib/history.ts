// history.ts — shared helpers for the in-process history time series.
import type { HistorySample } from "$lib/api";

export type RateKey =
	| "bytesUp"
	| "bytesDown"
	| "dns"
	| "conns"
	| "block"
	| "direct"
	| "proxy";

// ts renders a sample timestamp as HH:MM for axis labels. The label is
// display-only; chart linking keys on the full ISO timestamp instead.
export const ts = (iso: string) => new Date(iso).toTimeString().slice(0, 5);

// Delta fields are per-sample; convert to per-second rates using the actual
// sample spacing so the y-axis is meaningful regardless of poll cadence.
export function perSec(history: HistorySample[], key: RateKey): number[] {
	return rateSeries(history, [key])[key];
}

// rateSeries computes several rate series in one pass, parsing each sample
// timestamp once instead of once per requested key.
export function rateSeries(
	history: HistorySample[],
	keys: RateKey[],
): Record<RateKey, number[]> {
	const out = Object.fromEntries(
		keys.map((k) => [k, Array(history.length).fill(0)]),
	) as Record<RateKey, number[]>;
	let prevMs = 0;
	for (let i = 0; i < history.length; i++) {
		const sample = history[i];
		if (!sample) continue;
		const at = new Date(sample.at).getTime();
		if (i > 0) {
			const dt = (at - prevMs) / 1000;
			if (dt > 0) {
				for (const k of keys) out[k][i] = sample[k] / dt;
			}
		}
		prevMs = at;
	}
	// The first sample has no predecessor to diff against; backfill it with
	// the second rate so charts don't show a fake drop to zero at the edge.
	if (history.length > 1) {
		for (const k of keys) out[k][0] = out[k][1] ?? 0;
	}
	return out;
}
