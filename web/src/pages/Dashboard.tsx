import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useQuery } from "@tanstack/react-query"
import { CheckCircle2, CircleSlash, Clock, Search, Server, ShieldAlert, TriangleAlert } from "lucide-react"
import { api, enc, type Color, type HostSummary, type Overview } from "@/lib/api"
import { colorRank, since, timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Page, Empty } from "@/components/Page"
import { StatusBadge, StatusDot } from "@/components/status"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Switch } from "@/components/ui/switch"

function Stat({ label, value, icon: Icon, color, sub }: { label: string; value: number | string; icon: typeof Server; color?: string; sub?: string }) {
  return (
    <Card className="gap-2 py-4">
      <CardHeader className="items-center px-4">
        <CardDescription className="mt-0 text-xs font-medium tracking-wide uppercase">{label}</CardDescription>
        <Icon className={cn("size-4", color ?? "text-muted-foreground")} />
      </CardHeader>
      <CardContent className="px-4">
        <div className="text-2xl font-semibold tabular-nums">{value}</div>
        {sub && <div className="text-muted-foreground mt-0.5 text-xs">{sub}</div>}
      </CardContent>
    </Card>
  )
}

function Matrix({ hosts }: { hosts: HostSummary[] }) {
  const [q, setQ] = useState("")
  const [onlyProblems, setOnlyProblems] = useState(false)
  const navigate = useNavigate()
  const filtered = useMemo(() => {
    const re = q.trim().toLowerCase()
    return hosts.filter(
      (h) =>
        (!re || h.name.includes(re) || h.page.toLowerCase().includes(re) || Object.keys(h.tests).some((t) => t.includes(re))) &&
        (!onlyProblems || colorRank[h.color] >= colorRank.purple)
    )
  }, [hosts, q, onlyProblems])
  const tests = useMemo(() => {
    const s = new Set<string>()
    filtered.forEach((h) => Object.keys(h.tests).forEach((t) => s.add(t)))
    return [...s].sort()
  }, [filtered])
  const groups = useMemo(() => {
    const m = new Map<string, HostSummary[]>()
    filtered.forEach((h) => {
      const k = h.page || ""
      m.set(k, [...(m.get(k) ?? []), h])
    })
    return [...m.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
  }, [filtered])

  return (
    <Card>
      <CardHeader className="flex-col gap-3 sm:flex-row sm:items-center">
        <div>
          <CardTitle>Status matrix</CardTitle>
          <CardDescription>Every host and column, like the classic Xymon view</CardDescription>
        </div>
        <div className="flex w-full items-center gap-3 sm:w-auto">
          <label className="text-muted-foreground flex items-center gap-2 text-xs whitespace-nowrap">
            <Switch checked={onlyProblems} onCheckedChange={setOnlyProblems} /> Problems only
          </label>
          <div className="relative w-full sm:w-56">
            <Search className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
            <Input placeholder="Filter hosts or tests" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        </div>
      </CardHeader>
      <CardContent className="px-0">
        {filtered.length === 0 ? (
          <Empty title="No hosts match" />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b">
                  <th className="bg-card sticky left-0 z-10 px-5 py-2 text-left text-xs font-medium text-muted-foreground">Host</th>
                  {tests.map((t) => (
                    <th key={t} className="px-1 py-2 text-center text-xs font-medium text-muted-foreground">
                      <span className="inline-block max-w-16 truncate align-bottom" title={t}>{t}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {groups.map(([page, hs]) => (
                  <GroupRows key={page} page={page} hosts={hs} tests={tests} showHeader={groups.length > 1} onOpen={(h, t) => navigate(`/hosts/${enc(h)}/${enc(t)}`)} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function GroupRows({ page, hosts, tests, showHeader, onOpen }: { page: string; hosts: HostSummary[]; tests: string[]; showHeader: boolean; onOpen: (h: string, t: string) => void }) {
  return (
    <>
      {showHeader && (
        <tr className="bg-muted/40">
          <td colSpan={tests.length + 1} className="text-muted-foreground px-5 py-1.5 text-xs font-semibold tracking-wide uppercase">{page || "Other"}</td>
        </tr>
      )}
      {hosts.map((h) => (
        <tr key={h.name} className="hover:bg-muted/40 border-b last:border-0">
          <td className="bg-card sticky left-0 z-10 px-5 py-1.5 whitespace-nowrap">
            <Link to={`/hosts/${enc(h.name)}`} className="flex items-center gap-2 font-medium hover:underline">
              <StatusDot color={h.color} />
              {h.name}
            </Link>
          </td>
          {tests.map((t) => {
            const c = h.tests[t] as Color | undefined
            return (
              <td key={t} className="px-1 py-1.5 text-center">
                {c ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <button onClick={() => onOpen(h.name, t)} className="hover:bg-accent inline-flex size-6 items-center justify-center rounded">
                        <StatusDot color={c} className="size-3" />
                      </button>
                    </TooltipTrigger>
                    <TooltipContent>{h.name}.{t}: {c}</TooltipContent>
                  </Tooltip>
                ) : (
                  <span className="text-muted-foreground/30">·</span>
                )}
              </td>
            )
          })}
        </tr>
      ))}
    </>
  )
}

export default function Dashboard() {
  const { data: ov, isLoading } = useQuery({ queryKey: ["overview"], queryFn: () => api.get<Overview>("/overview") })
  const { data: hosts = [] } = useQuery({ queryKey: ["hosts"], queryFn: () => api.get<HostSummary[]>("/hosts") })
  const problems = useMemo(
    () => [...(ov?.problems ?? [])].sort((a, b) => colorRank[b.color] - colorRank[a.color] || a.lastChange.localeCompare(b.lastChange)),
    [ov]
  )

  if (isLoading || !ov) {
    return (
      <Page>
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-5">{Array.from({ length: 5 }).map((_, i) => <Skeleton key={i} className="h-24" />)}</div>
        <Skeleton className="h-64" />
      </Page>
    )
  }
  const sc = ov.statusCounts
  return (
    <Page>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-5">
        <Stat label="Hosts" value={ov.hosts} icon={Server} sub={`${ov.tests} checks`} />
        <Stat label="Critical" value={sc.red} icon={ShieldAlert} color="text-status-red" sub={`${ov.hostCounts.red} hosts`} />
        <Stat label="Warning" value={sc.yellow} icon={TriangleAlert} color="text-status-yellow" sub={`${ov.hostCounts.yellow} hosts`} />
        <Stat label="Stale" value={sc.purple} icon={Clock} color="text-status-purple" sub="no recent report" />
        <Stat label="OK" value={sc.green} icon={CheckCircle2} color="text-status-green" sub={`${sc.blue} disabled · ${sc.clear} clear`} />
      </div>

      <div className="grid gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader>
            <div>
              <CardTitle>Problems</CardTitle>
              <CardDescription>Checks that are critical, warning or stale</CardDescription>
            </div>
            {problems.length > 0 && <Badge variant="secondary">{problems.length}</Badge>}
          </CardHeader>
          <CardContent className="px-0">
            {problems.length === 0 ? (
              <Empty icon={<CheckCircle2 className="text-status-green size-8" />} title="All systems operational">
                No checks are reporting problems.
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-5">Status</TableHead>
                    <TableHead>Host</TableHead>
                    <TableHead>Check</TableHead>
                    <TableHead className="w-full">Summary</TableHead>
                    <TableHead className="pr-5 text-right">Duration</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {problems.map((p) => (
                    <TableRow key={p.host + p.test} className={cn(p.acked && "opacity-60")}>
                      <TableCell className="pl-5"><StatusBadge color={p.color} /></TableCell>
                      <TableCell className="font-medium"><Link className="hover:underline" to={`/hosts/${enc(p.host)}`}>{p.host}</Link></TableCell>
                      <TableCell><Link className="hover:underline" to={`/hosts/${enc(p.host)}/${enc(p.test)}`}>{p.test}</Link></TableCell>
                      <TableCell className="text-muted-foreground max-w-0 truncate" title={p.summary}>
                        {p.acked && <Badge variant="outline" className="mr-2">acked</Badge>}
                        {p.summary}
                      </TableCell>
                      <TableCell className="text-muted-foreground pr-5 text-right tabular-nums">{since(p.lastChange)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <div>
              <CardTitle>Recent changes</CardTitle>
              <CardDescription>Latest color transitions</CardDescription>
            </div>
            <Link to="/events" className="text-muted-foreground text-xs hover:underline">View all</Link>
          </CardHeader>
          <CardContent>
            {ov.recent.length === 0 ? (
              <Empty icon={<CircleSlash className="size-6" />} title="No events yet" />
            ) : (
              <ul className="space-y-3">
                {ov.recent.map((e) => (
                  <li key={e.id} className="flex items-start gap-3 text-sm">
                    <div className="mt-1 flex items-center gap-1">
                      <StatusDot color={e.prevColor} className="size-2 opacity-50" />
                      <span className="text-muted-foreground text-xs">→</span>
                      <StatusDot color={e.color} className="size-2.5" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <Link to={`/hosts/${enc(e.host)}/${enc(e.test)}`} className="block truncate font-medium hover:underline" title={`${e.host}.${e.test}`}>{e.host}.{e.test}</Link>
                      <div className="text-muted-foreground truncate text-xs" title={e.summary}>{e.summary || "—"}</div>
                    </div>
                    <span className="text-muted-foreground shrink-0 text-xs">{timeAgo(e.time)}</span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      {hosts.length > 0 ? (
        <Matrix hosts={hosts} />
      ) : (
        <Card>
          <CardContent>
            <Empty icon={<Server className="size-8" />} title="No hosts yet">
              Point any Xymon client at this server (port 1984, or xymons:// on 1985) and hosts appear automatically. You can also add hosts or import a hosts.cfg on the Hosts page.
            </Empty>
          </CardContent>
        </Card>
      )}
    </Page>
  )
}
