export type Category = "block" | "direct" | "proxy";

export type DomainSort = "bytes" | "recent" | "conns";

export type Source = "all" | "http" | "https" | "socks5" | "dns";

export interface Status {
	version: string;
	date: string;
	uptime: number;
	rules: Record<Category, number>;
}

export interface DomainStat {
	domain: string;
	conns: number;
	bytesUp: number;
	bytesDown: number;
	lastSeen: string;
}

export interface ClientStat {
	ip: string;
	conns: number;
	bytesUp: number;
	bytesDown: number;
	lastSeen: string;
}

export interface TrafficSnapshot {
	uptime: number;
	dnsQueries: number;
	conns: { http: number; https: number; socks5: number };
	active: { http: number; https: number; socks5: number };
	rates: {
		bytesUpPerSec: number;
		bytesDownPerSec: number;
		dnsPerSec: number;
		connsPerSec: number;
	};
	ruleHits: { block: number; direct: number; proxy: number };
	system: { goroutines: number; heapAlloc: number };
	bytesUp: number;
	bytesDown: number;
	domains: DomainStat[];
	clients: ClientStat[];
}

export interface HistorySample {
	at: string;
	bytesUp: number;
	bytesDown: number;
	dns: number;
	conns: number;
	active: number;
	activeHttp: number;
	activeHttps: number;
	activeSocks: number;
	block: number;
	direct: number;
	proxy: number;
}

export interface History {
	samples: HistorySample[];
}

export interface RulesResponse {
	category: Category;
	rules: string[];
	total: number;
	offset: number;
	limit: number;
}

export interface RulesQuery {
	q?: string;
	offset?: number;
	limit?: number;
}

export class ApiError extends Error {
	constructor(
		public status: number,
		message: string,
	) {
		super(message);
	}
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	const resp = await fetch(path, {
		headers: { "Content-Type": "application/json" },
		...init,
	});
	if (!resp.ok) {
		let message = `HTTP ${resp.status}`;
		try {
			const body = await resp.json();
			if (body && typeof body.error === "string") message = body.error;
		} catch {
			// keep the generic message
		}
		throw new ApiError(resp.status, message);
	}
	if (resp.status === 204) return undefined as T;
	return (await resp.json()) as T;
}

export const api = {
	login: (password: string) =>
		request<void>("/api/session", {
			method: "POST",
			body: JSON.stringify({ password }),
		}),
	logout: () => request<void>("/api/session", { method: "DELETE" }),
	status: () => request<Status>("/api/status"),
	rules: (category: Category, params?: RulesQuery) => {
		const sp = new URLSearchParams({ category });
		if (params?.q) sp.set("q", params.q);
		if (params?.offset != null) sp.set("offset", String(params.offset));
		if (params?.limit != null) sp.set("limit", String(params.limit));
		return request<RulesResponse>(`/api/rules?${sp}`);
	},
	addRules: (category: Category, rules: string[]) =>
		request<void>("/api/rules", {
			method: "POST",
			body: JSON.stringify({ category, rules }),
		}),
	removeRules: (category: Category, rules: string[]) =>
		request<void>("/api/rules", {
			method: "DELETE",
			body: JSON.stringify({ category, rules }),
		}),
	traffic: (sort?: DomainSort, source?: Source, client?: string) => {
		const params: string[] = [];
		if (sort) params.push(`sort=${sort}`);
		if (source && source !== "all") params.push(`source=${source}`);
		if (client) params.push(`client=${encodeURIComponent(client)}`);
		return request<TrafficSnapshot>(params.length ? `/api/traffic?${params.join("&")}` : "/api/traffic");
	},
	history: () => request<History>("/api/history"),
};
