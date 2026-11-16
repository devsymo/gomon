import type { Color } from "./api"

export const COLORS: Color[] = ["red", "yellow", "purple", "blue", "clear", "green"]

export const colorRank: Record<Color, number> = { green: 0, clear: 1, blue: 2, purple: 3, yellow: 4, red: 5 }

export const colorLabel: Record<Color, string> = {
  green: "OK",
  yellow: "Warning",
  red: "Critical",
  purple: "Stale",
  blue: "Disabled",
  clear: "No data",
}

export function timeAgo(iso: string | number | undefined): string {
  if (!iso) return "—"
  const t = typeof iso === "number" ? iso : new Date(iso).getTime()
  const s = Math.round((Date.now() - t) / 1000)
  if (s < 0) return "in " + duration(-s)
  if (s < 5) return "just now"
  return duration(s) + " ago"
}

export function duration(s: number): string {
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h ${m % 60}m`
  const d = Math.floor(h / 24)
  return `${d}d ${h % 24}h`
}

export function since(iso: string): string {
  return duration(Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000)))
}

export function fmtDate(iso: string | number | undefined): string {
  if (!iso) return "—"
  const d = new Date(iso)
  if (d.getFullYear() >= 9999) return "until OK"
  return d.toLocaleString()
}

export function fmtNumber(v: number): string {
  const a = Math.abs(v)
  if (a >= 1e9) return (v / 1e9).toFixed(1) + "G"
  if (a >= 1e6) return (v / 1e6).toFixed(1) + "M"
  if (a >= 1e4) return (v / 1e3).toFixed(1) + "k"
  if (a >= 100) return v.toFixed(0)
  if (a >= 1) return v.toFixed(2).replace(/\.?0+$/, "")
  return v.toPrecision(2)
}
