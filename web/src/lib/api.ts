export type Color = "green" | "yellow" | "red" | "purple" | "blue" | "clear"

export interface Status {
  host: string
  test: string
  color: Color
  summary: string
  message?: string
  lastChange: string
  lastUpdate: string
  validUntil: string
  sender: string
  transport: string
  group?: string
  cookie?: number
  ackUntil?: string
  ackMsg?: string
  ackBy?: string
  disabledUntil?: string
  disableMsg?: string
  acked: boolean
  disabled: boolean
}

export interface Host {
  name: string
  ip: string
  tags: string
  description: string
  page: string
  group: string
  os: string
  class: string
  notes: string
  createdAt: string
  updatedAt: string
}

export interface HostSummary extends Host {
  lastSeen?: string
  color: Color
  tests: Record<string, Color>
  counts: Partial<Record<Color, number>>
}

export interface HistoryEvent {
  id: number
  host: string
  test: string
  time: string
  color: Color
  prevColor: Color
  summary: string
  message?: string
}

export interface Overview {
  statusCounts: Record<Color, number>
  hostCounts: Record<Color, number>
  hosts: number
  tests: number
  problems: Status[] | null
  recent: HistoryEvent[]
}

export interface MetricSeries {
  test: string
  name: string
  points: [number, number][]
}

export interface ClientData {
  host: string
  collector: string
  time: string
  os: string
  class: string
  sender: string
  transport: string
  sections: { name: string; content: string }[] | null
}

export interface SNMPOID {
  name: string
  oid: string
  rate: boolean
  warn?: number | null
  crit?: number | null
  below: boolean
  test?: string
}

export interface SNMPTarget {
  id: number
  host: string
  address: string
  port: number
  version: "1" | "2c" | "3"
  community?: string
  v3: {
    username: string
    securityLevel: string
    authProtocol: string
    authPassword?: string
    privProtocol: string
    privPassword?: string
    contextName?: string
  }
  intervalSeconds: number
  pollInterfaces: boolean
  oids: SNMPOID[]
  enabled: boolean
}

export interface AlertChannel {
  id: number
  name: string
  type: "webhook" | "slack" | "discord" | "ntfy" | "email"
  config: Record<string, string>
  enabled: boolean
}

export interface AlertRule {
  id: number
  name: string
  hostPattern: string
  testPattern: string
  excludeHosts: string
  excludeTests: string
  minColor: "yellow" | "red" | "purple"
  delayMinutes: number
  repeatMinutes: number
  notifyRecovery: boolean
  channelIds: number[]
  enabled: boolean
}

export interface ThemeDef {
  id: string
  name: string
  description: string
  light: Record<string, string>
  dark: Record<string, string>
}

export interface AuthInfo {
  setupRequired: boolean
  oidcEnabled: boolean
  oidcLabel: string
  builtinLogin: boolean
}

export interface User {
  username: string
  method: "password" | "oidc" | "apikey"
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  let data: unknown = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    const msg = (data && typeof data === "object" && "error" in data ? String((data as { error: unknown }).error) : res.statusText) || "Request failed"
    if (res.status === 401 && !path.startsWith("/auth/")) {
      window.dispatchEvent(new Event("gomon:unauthorized"))
    }
    throw new ApiError(res.status, msg)
  }
  return data as T
}

export const api = {
  get: <T>(p: string) => request<T>("GET", p),
  post: <T>(p: string, b: unknown = {}) => request<T>("POST", p, b),
  put: <T>(p: string, b: unknown) => request<T>("PUT", p, b),
  del: <T>(p: string) => request<T>("DELETE", p),
}

export const enc = encodeURIComponent
