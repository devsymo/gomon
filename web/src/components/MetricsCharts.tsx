import { useEffect, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { LineChart as LineIcon } from "lucide-react"
import { api, enc, type MetricSeries } from "@/lib/api"
import { fmtNumber } from "@/lib/format"
import { useTheme } from "@/lib/theme"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { Empty } from "@/components/Page"

const RANGES = ["1h", "6h", "24h", "7d", "30d", "90d"] as const
// Distinct categorical colors; resolved from CSS variables because SVG
// presentation attributes cannot use var().
const PALETTE_VARS = ["--chart-2", "--status-green", "--status-yellow", "--status-purple", "--status-red", "--chart-1", "--status-blue", "--chart-4"]
const MAX_SERIES = 8

function readPalette() {
  const cs = getComputedStyle(document.documentElement)
  return PALETTE_VARS.map((v) => cs.getPropertyValue(v).trim() || "#888")
}

function usePalette() {
  const { themeId, resolvedDark } = useTheme()
  const [palette, setPalette] = useState(readPalette)
  useEffect(() => {
    // The theme provider applies variables in its own effect; read after it.
    const id = requestAnimationFrame(() => setPalette(readPalette()))
    return () => cancelAnimationFrame(id)
  }, [themeId, resolvedDark])
  return palette
}

const titles: Record<string, string> = {
  cpu: "CPU", disk: "Disk usage (%)", inode: "Inode usage (%)", memory: "Memory", conn: "Ping round-trip (ms)",
  http: "HTTP response time (s)", if: "Interface traffic (bit/s)", snmp: "SNMP", sslcert: "Certificate days left", procs: "Processes",
}

// Series that read best on their own chart.
function splitGroups(test: string, series: MetricSeries[]): { key: string; title: string; series: MetricSeries[] }[] {
  if (test === "cpu") {
    const load = series.filter((s) => s.name.startsWith("load"))
    const util = series.filter((s) => s.name.startsWith("cpu_"))
    const rest = series.filter((s) => !load.includes(s) && !util.includes(s) && s.name !== "uptime_days")
    return [
      { key: "cpu-load", title: "Load average", series: load },
      { key: "cpu-util", title: "CPU utilisation (%)", series: util },
      { key: "cpu-other", title: "Processes", series: rest },
    ].filter((g) => g.series.length)
  }
  if (test === "memory") {
    return [
      { key: "mem-pct", title: "Memory used (%)", series: series.filter((s) => s.name.endsWith("_pct")) },
      { key: "mem-mb", title: "Memory used (MB)", series: series.filter((s) => s.name.endsWith("_mb")) },
    ].filter((g) => g.series.length)
  }
  if (test === "snmp") {
    const rest = series.filter((s) => s.name !== "uptime_days")
    return rest.length ? [{ key: "snmp", title: "SNMP values", series: rest }] : []
  }
  return [{ key: test, title: titles[test] ?? test, series }]
}

function Chart({ title, series }: { title: string; series: MetricSeries[] }) {
  const PALETTE = usePalette()
  const top = useMemo(() => {
    const scored = series.map((s) => ({ s, max: Math.max(...s.points.map((p) => p[1])) }))
    scored.sort((a, b) => b.max - a.max)
    return scored.slice(0, MAX_SERIES).map((x) => x.s)
  }, [series])
  const data = useMemo(() => {
    const byTs = new Map<number, Record<string, number>>()
    top.forEach((s) =>
      s.points.forEach(([t, v]) => {
        const row = byTs.get(t) ?? { t }
        row[s.name] = v
        byTs.set(t, row)
      })
    )
    return [...byTs.values()].sort((a, b) => a.t - b.t)
  }, [top])
  const spanMs = data.length > 1 ? data[data.length - 1].t - data[0].t : 0
  const fmtTime = (t: number) => {
    const d = new Date(t)
    return spanMs > 2 * 86400_000 ? d.toLocaleDateString(undefined, { month: "short", day: "numeric" }) : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
  }
  return (
    <Card className="gap-2 py-4">
      <CardHeader className="px-4">
        <CardTitle className="text-sm">{title}</CardTitle>
        {series.length > MAX_SERIES && <CardDescription className="mt-0 text-xs">top {MAX_SERIES} of {series.length}</CardDescription>}
      </CardHeader>
      <CardContent className="h-52 px-2">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
            <defs>
              {top.map((s, i) => (
                <linearGradient key={s.name} id={`g-${title}-${i}`.replace(/[^a-zA-Z0-9-]/g, "")} x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={PALETTE[i % PALETTE.length]} stopOpacity={0.3} />
                  <stop offset="100%" stopColor={PALETTE[i % PALETTE.length]} stopOpacity={0} />
                </linearGradient>
              ))}
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
            <XAxis dataKey="t" type="number" domain={["dataMin", "dataMax"]} tickFormatter={fmtTime} tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" minTickGap={40} />
            <YAxis tickFormatter={fmtNumber} tick={{ fontSize: 11, fill: "var(--muted-foreground)" }} stroke="var(--border)" width={44} />
            <Tooltip
              contentStyle={{ background: "var(--popover)", border: "1px solid var(--border)", borderRadius: 8, fontSize: 12, color: "var(--popover-foreground)" }}
              labelFormatter={(t) => new Date(Number(t)).toLocaleString()}
              formatter={(v: number, n: string) => [fmtNumber(v), n]}
            />
            {top.map((s, i) => (
              <Area
                key={s.name}
                type="monotone"
                dataKey={s.name}
                stroke={PALETTE[i % PALETTE.length]}
                fill={`url(#${`g-${title}-${i}`.replace(/[^a-zA-Z0-9-]/g, "")})`}
                strokeWidth={1.5}
                dot={false}
                connectNulls
                isAnimationActive={false}
              />
            ))}
          </AreaChart>
        </ResponsiveContainer>
      </CardContent>
      <div className="flex flex-wrap gap-x-3 gap-y-1 px-4 text-xs">
        {top.map((s, i) => (
          <span key={s.name} className="text-muted-foreground flex items-center gap-1.5">
            <span className="size-2 rounded-full" style={{ background: PALETTE[i % PALETTE.length] }} />
            {s.name}
          </span>
        ))}
      </div>
    </Card>
  )
}

export function MetricsCharts({ host, test, compact }: { host: string; test?: string; compact?: boolean }) {
  const [range, setRange] = useState<(typeof RANGES)[number]>("24h")
  const { data, isLoading } = useQuery({
    queryKey: ["metrics", host, test ?? "", range],
    queryFn: () => api.get<{ series: MetricSeries[] }>(`/hosts/${enc(host)}/metrics?range=${range}${test ? `&test=${enc(test)}` : ""}`),
    refetchInterval: 60_000,
  })
  const groups = useMemo(() => {
    const byTest = new Map<string, MetricSeries[]>()
    ;(data?.series ?? []).forEach((s) => byTest.set(s.test, [...(byTest.get(s.test) ?? []), s]))
    return [...byTest.entries()].sort(([a], [b]) => a.localeCompare(b)).flatMap(([t, ss]) => splitGroups(t, ss))
  }, [data])

  const picker = (
    <Tabs value={range} onValueChange={(v) => setRange(v as (typeof RANGES)[number])}>
      <TabsList>
        {RANGES.map((r) => <TabsTrigger key={r} value={r} className="px-2 text-xs">{r}</TabsTrigger>)}
      </TabsList>
    </Tabs>
  )
  if (isLoading) return <div className="grid gap-4 lg:grid-cols-2"><Skeleton className="h-64" /><Skeleton className="h-64" /></div>
  if (groups.length === 0) {
    if (compact) return null
    return (
      <div className="space-y-4">
        <div className="flex justify-end">{picker}</div>
        <Empty icon={<LineIcon className="size-8" />} title="No metrics in this range">
          Metrics come from client data (cpu, disk, memory), network tests, SNMP, "data HOST.trends" messages and "name : value" lines in plugin statuses.
        </Empty>
      </div>
    )
  }
  return (
    <div className="space-y-4">
      <div className="flex justify-end">{picker}</div>
      <div className="grid gap-4 lg:grid-cols-2">
        {groups.map((g) => <Chart key={g.key} title={g.title} series={g.series} />)}
      </div>
    </div>
  )
}
