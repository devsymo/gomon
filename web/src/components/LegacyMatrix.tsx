import { Link } from "react-router-dom"
import { Check } from "lucide-react"
import { enc, type Color, type HostSummary, type Status } from "@/lib/api"
import { colorRank, since } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

// Literal class names so Tailwind generates them.
const bg: Record<Color, string> = {
  green: "bg-status-green", yellow: "bg-status-yellow", red: "bg-status-red",
  purple: "bg-status-purple", blue: "bg-status-blue", clear: "bg-status-clear",
}
export const bannerTint: Record<Color, string> = {
  green: "bg-status-green/12 border-status-green/40",
  yellow: "bg-status-yellow/12 border-status-yellow/50",
  red: "bg-status-red/12 border-status-red/50",
  purple: "bg-status-purple/12 border-status-purple/50",
  blue: "bg-status-blue/12 border-status-blue/40",
  clear: "bg-muted border-border",
}

export interface MatrixHost {
  name: string
  description?: string
  worst: Color
  statuses: Record<string, Status>
}

export interface MatrixGroup {
  key: string
  title: string
  hosts: MatrixHost[]
}

export function worstOf(colors: Iterable<Color>, empty: Color = "clear"): Color {
  let w: Color | null = null
  for (const c of colors) if (w === null || colorRank[c] > colorRank[w]) w = c
  return w ?? empty
}

/** Joins hosts with their statuses. */
export function buildHosts(hosts: HostSummary[], statuses: Status[]): Map<string, MatrixHost> {
  const m = new Map<string, MatrixHost>()
  hosts.forEach((h) => m.set(h.name, { name: h.name, description: h.description, worst: "clear", statuses: {} }))
  statuses.forEach((s) => {
    const h = m.get(s.host)
    if (h) h.statuses[s.test] = s
  })
  m.forEach((h) => (h.worst = worstOf(Object.values(h.statuses).map((s) => s.color))))
  return m
}

/** A glossy round icon in the spirit of Xymon's status GIFs. */
export function LegacyIcon({ color, acked, recent, size = "md" }: { color: Color; acked?: boolean; recent?: boolean; size?: "md" | "lg" }) {
  return (
    <span className={cn("relative inline-flex shrink-0", size === "lg" ? "size-6" : "size-4")}>
      {recent && (color === "red" || color === "yellow" || color === "purple") && (
        <span className={cn("absolute inset-0 animate-ping rounded-full opacity-40", bg[color])} />
      )}
      <span
        className={cn("relative inline-flex size-full items-center justify-center rounded-full shadow-[inset_0_-1px_2px_rgba(0,0,0,0.35)]", bg[color])}
        style={{ backgroundImage: "radial-gradient(circle at 35% 30%, rgba(255,255,255,0.6), rgba(255,255,255,0) 55%)" }}
      >
        {acked && <Check className="size-2.5 text-white drop-shadow" strokeWidth={4} />}
      </span>
    </span>
  )
}

const RECENT_MS = 60 * 60 * 1000

/**
 * The classic Xymon matrix: one table per group heading, each with its own
 * columns, rotated column headers and one icon per host/test.
 */
export function LegacyMatrix({ groups, columnFilter }: { groups: MatrixGroup[]; columnFilter?: (test: string, hosts: MatrixHost[]) => boolean }) {
  const now = Date.now()
  return (
    <div className="space-y-6">
      {groups.map((g) => {
        let tests = [...new Set(g.hosts.flatMap((h) => Object.keys(h.statuses)))].sort()
        if (columnFilter) tests = tests.filter((t) => columnFilter(t, g.hosts))
        if (g.hosts.length === 0) return null
        return (
          <section key={g.key} className="bg-card overflow-hidden rounded-lg border">
            {g.title && (
              <div className="bg-muted/60 flex items-center gap-2 border-b px-4 py-2">
                <LegacyIcon color={worstOf(g.hosts.map((h) => h.worst))} />
                <h3 className="text-sm font-semibold tracking-wide">{g.title}</h3>
                <span className="text-muted-foreground ml-auto text-xs">{g.hosts.length} host{g.hosts.length === 1 ? "" : "s"}</span>
              </div>
            )}
            <div className="overflow-x-auto">
              <table className="mx-auto font-mono text-xs">
                <thead>
                  <tr>
                    <th className="bg-card sticky left-0 z-10" />
                    {tests.map((t) => (
                      <th key={t} className="px-1.5 pt-3 pb-2 align-bottom font-medium">
                        <div className="text-muted-foreground mx-auto max-h-28 overflow-hidden [writing-mode:vertical-rl] rotate-180 whitespace-nowrap" title={t}>
                          {t}
                        </div>
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {g.hosts.map((h) => (
                    <tr key={h.name} className="hover:bg-accent/40 border-t">
                      <td className="bg-card sticky left-0 z-10 max-w-72 px-4 py-1.5 text-right whitespace-nowrap">
                        <Link to={`/hosts/${enc(h.name)}`} className="hover:underline" title={h.description || h.name}>
                          {h.name}
                        </Link>
                      </td>
                      {tests.map((t) => {
                        const s = h.statuses[t]
                        return (
                          <td key={t} className="px-1.5 py-1.5 text-center">
                            {s ? (
                              <Tooltip>
                                <TooltipTrigger asChild>
                                  <Link to={`/hosts/${enc(h.name)}/${enc(t)}`} className="inline-flex rounded-full p-0.5 hover:ring-2 hover:ring-ring/50">
                                    <LegacyIcon color={s.color} acked={s.acked} recent={now - new Date(s.lastChange).getTime() < RECENT_MS} />
                                  </Link>
                                </TooltipTrigger>
                                <TooltipContent className="max-w-sm">
                                  <div className="font-semibold">{h.name}.{t} · {s.color} for {since(s.lastChange)}{s.acked && " · acked"}</div>
                                  <div className="opacity-80">{s.summary}</div>
                                </TooltipContent>
                              </Tooltip>
                            ) : (
                              <span className="text-muted-foreground/40">-</span>
                            )}
                          </td>
                        )
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )
      })}
    </div>
  )
}

export function Legend() {
  const items: [Color, string][] = [["red", "Critical"], ["yellow", "Warning"], ["purple", "No report"], ["blue", "Disabled"], ["clear", "No data"], ["green", "OK"]]
  return (
    <div className="text-muted-foreground flex flex-wrap items-center justify-center gap-x-4 gap-y-2 text-xs">
      {items.map(([c, l]) => (
        <span key={c} className="flex items-center gap-1.5"><LegacyIcon color={c} />{l}</span>
      ))}
      <span className="flex items-center gap-1.5"><LegacyIcon color="red" acked />Acknowledged</span>
      <span className="flex items-center gap-1.5"><LegacyIcon color="yellow" recent />Changed in the last hour</span>
    </div>
  )
}
