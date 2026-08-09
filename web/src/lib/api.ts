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
	lastClientIP: string;
}

export interface ClientStat {
	ip: string;
	conns: number;
	bytesUp: number;
	bytesDown: number;
	lastSeen: string;
}

export interface BlockedStat {
	domain: string;
	count: number;
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
	blocked: BlockedStat[];
	system: { goroutines: number; heapAlloc: number };
	bytesUp: number;
	bytesDown: number;
	domains: DomainStat[];
	clients: ClientStat[];
}

export interface Totals {
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
	rules: RuleEntry[];
	total: number;
	offset: number;
	limit: number;
}

export interface RuleEntry {
	rule: string;
	count: number;
	lastSeen?: string;
}

export interface RuleHit {
	rule: string;
	count: number;
	lastSeen: string;
}

export interface CategoryTest {
	category: Category;
	matched: boolean;
	rule: string;
}

export interface DomainTest {
	domain: string;
	route: "block" | "direct" | "proxy" | "auto";
	matches: CategoryTest[];
	note?: string;
}

export interface RulesQuery {
	q?: string;
	offset?: number;
	limit?: number;
	sort?: RuleSort;
	dir?: RuleSortDir;
}

export type RuleSort = "default" | "rule" | "hits" | "last_seen";

export type RuleSortDir = "asc" | "desc";

export interface RuleDelta {
	add: string[];
	remove: string[];
}

export interface RuleChangeSet {
	persistent: boolean;
	revision: number;
	rules: Record<Category, RuleDelta>;
}

export type ConfigApplyMode = "immediate" | "restart" | "readonly";

export interface ConfigField {
	key: string;
	value?: string;
	editable: boolean;
	applyMode: ConfigApplyMode;
	source: "config" | "override";
	constraint?: string;
	secret?: boolean;
	configured?: boolean;
	type?: string;
	options?: string[];
}

export interface ConfigSection {
	name: string;
	fields: ConfigField[];
}

export interface ConfigView {
	revision: number;
	sections: ConfigSection[];
}

// ConfigChanges is the whitelisted PATCH payload: a present key replaces the
// override (empty string or empty list clears it), an absent key leaves it
// unchanged. List fields (rule sources) carry the full inline lists.
export interface ConfigChanges {
	log_level?: string;
	dns_upstream?: string;
	dns_fallback?: string;
	remote_type?: string;
	remote_addr?: string;
	remote_tls_server_name?: string;
	remote_tls_client_hello?: string;
	remote_tls_insecure_skip_verify?: string;
	dns_serve?: string;
	dns_serve6?: string;
	socks5_addr?: string;
	admin_session_file?: string;
	admin_disable_session_persistence?: string;
	admin_cookie_secure?: string;
	admin_state_file?: string;
	router_block_file?: string;
	router_block_file_prefix?: string;
	router_block_file_skip_rules?: string[];
	router_block_rules?: string[];
	router_direct_file?: string;
	router_direct_file_prefix?: string;
	router_direct_file_skip_rules?: string[];
	router_direct_rules?: string[];
	router_proxy_file?: string;
	router_proxy_file_prefix?: string;
	router_proxy_file_skip_rules?: string[];
	router_proxy_rules?: string[];
	router_country_mmdb?: string;
	router_country_file?: string;
	router_country_rules?: string[];
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

export const probeSession = () =>
	request<void>("/api/session", { cache: "no-store" });

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
		if (params?.sort && params.sort !== "default") {
			sp.set("sort", params.sort);
			if (params?.dir) sp.set("dir", params.dir);
		}
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
	rulesTest: (domain: string) =>
		request<DomainTest>(`/api/rules/test?domain=${encodeURIComponent(domain)}`),
	rulesChanges: () => request<RuleChangeSet>("/api/rules/changes"),
	resetRules: (category?: Category) =>
		request<void>("/api/rules/reset", {
			method: "POST",
			body: JSON.stringify({ category: category ?? "" }),
		}),
	ruleMiss: (sort: "count" | "recent", limit?: number) =>
		request<RuleHit[]>(
			`/api/rules/miss?sort=${sort}${limit ? `&limit=${limit}` : ""}`,
		),
	config: () => request<ConfigView>("/api/config"),
	patchConfig: (revision: number, changes: ConfigChanges) =>
		request<ConfigView>("/api/config", {
			method: "PATCH",
			body: JSON.stringify({ revision, changes }),
		}),
	restart: () =>
		request<{ status: string }>("/api/restart", { method: "POST" }),
	traffic: (sort?: DomainSort, source?: Source, client?: string) => {
		const params: string[] = [];
		if (sort) params.push(`sort=${sort}`);
		if (source && source !== "all") params.push(`source=${source}`);
		if (client) params.push(`client=${encodeURIComponent(client)}`);
		return request<TrafficSnapshot>(
			params.length ? `/api/traffic?${params.join("&")}` : "/api/traffic",
		);
	},
	history: () => request<History>("/api/history"),
};
