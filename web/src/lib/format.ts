export function formatBytes(n: number): string {
	if (!Number.isFinite(n) || n < 0) return "-";
	if (n < 1024) return `${n} B`;
	const units = ["KB", "MB", "GB", "TB", "PB"];
	let v = n;
	let i = -1;
	do {
		v /= 1024;
		i++;
	} while (v >= 1024 && i < units.length - 1);
	return `${v.toFixed(1)} ${units[i]}`;
}

export function formatUptime(seconds: number): string {
	if (!Number.isFinite(seconds) || seconds < 0) return "-";
	const d = Math.floor(seconds / 86400);
	const h = Math.floor((seconds % 86400) / 3600);
	const m = Math.floor((seconds % 3600) / 60);
	const s = Math.floor(seconds % 60);
	if (d > 0) return `${d}d ${h}h`;
	if (h > 0) return `${h}h ${m}m`;
	if (m > 0) return `${m}m ${s}s`;
	return `${s}s`;
}

export function formatCount(n: number): string {
	if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
	if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
	if (n >= 1e3) return `${(n / 1e3).toFixed(1)}K`;
	return `${n}`;
}

// formatTime renders a compact timestamp: HH:MM:SS for today, with the date
// prepended for older entries. Admin tables refresh every few seconds, so the
// full locale date-time string is mostly noise.
export function formatTime(iso: string): string {
	const t = new Date(iso);
	if (Number.isNaN(t.getTime())) return "-";
	const pad = (n: number) => String(n).padStart(2, "0");
	const hms = `${pad(t.getHours())}:${pad(t.getMinutes())}:${pad(t.getSeconds())}`;
	if (t.toDateString() === new Date().toDateString()) return hms;
	return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${hms}`;
}
