export type Category = 'block' | 'direct' | 'proxy'

export interface Status {
  version: string
  date: string
  uptime: number
  rules: Record<Category, number>
}

export interface DomainStat {
  domain: string
  conns: number
  bytesUp: number
  bytesDown: number
  lastSeen: string
}

export interface TrafficSnapshot {
  uptime: number
  dnsQueries: number
  conns: { http: number; https: number; socks5: number }
  bytesUp: number
  bytesDown: number
  domains: DomainStat[]
}

export interface RulesResponse {
  category: Category
  rules: string[]
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!resp.ok) {
    let message = `HTTP ${resp.status}`
    try {
      const body = await resp.json()
      if (body && typeof body.error === 'string') message = body.error
    } catch {
      // keep the generic message
    }
    throw new ApiError(resp.status, message)
  }
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}

export const api = {
  login: (password: string) =>
    request<void>('/api/session', { method: 'POST', body: JSON.stringify({ password }) }),
  logout: () => request<void>('/api/session', { method: 'DELETE' }),
  status: () => request<Status>('/api/status'),
  rules: (category: Category) => request<RulesResponse>(`/api/rules?category=${category}`),
  addRules: (category: Category, rules: string[]) =>
    request<RulesResponse>('/api/rules', {
      method: 'POST',
      body: JSON.stringify({ category, rules }),
    }),
  removeRules: (category: Category, rules: string[]) =>
    request<RulesResponse>('/api/rules', {
      method: 'DELETE',
      body: JSON.stringify({ category, rules }),
    }),
  traffic: () => request<TrafficSnapshot>('/api/traffic'),
}
