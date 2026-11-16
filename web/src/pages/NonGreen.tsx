import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { PartyPopper } from "lucide-react"
import { api, type Color, type HostSummary, type Status } from "@/lib/api"
import { colorLabel, colorRank, fmtDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Page, Empty } from "@/components/Page"
import { LegacyIcon, LegacyMatrix, Legend, bannerTint, buildHosts, worstOf, type MatrixGroup } from "@/components/LegacyMatrix"
import { Card, CardContent } from "@/components/ui/card"
import { Switch } from "@/components/ui/switch"
import { Skeleton } from "@/components/ui/skeleton"

const CHOICES: Color[] = ["red", "yellow", "purple", "blue", "clear"]

function usePersistedColors(): [Color[], (c: Color[]) => void] {
  const [v, setV] = useState<Color[]>(() => {
    try {
      return JSON.parse(localStorage.getItem("gomon-nongreen-colors") ?? "") as Color[]
    } catch {
      return ["red", "yellow", "purple"]
    }
  })
  return [v, (c) => {
    setV(c)
    try {
      localStorage.setItem("gomon-nongreen-colors", JSON.stringify(c))
    } catch {
      /* ignore */
    }
  }]
}

export default function NonGreen() {
  const { data: hosts, isLoading: l1 } = useQuery({ queryKey: ["hosts"], queryFn: () => api.get<HostSummary[]>("/hosts") })
  const { data: statuses, isLoading: l2 } = useQuery({ queryKey: ["statuses"], queryFn: () => api.get<Status[]>("/statuses") })
  const { data: titles = {} } = useQuery({ queryKey: ["pages"], queryFn: () => api.get<Record<string, string>>("/pages") })
  const [colors, setColors] = usePersistedColors()
  const [hideAcked, setHideAcked] = useState(false)

  const view = useMemo(() => {
    if (!hosts || !statuses) return null
    const all = buildHosts(hosts, statuses)
    const want = new Set(colors)
    const matches = (s: Status) => want.has(s.color) && !(hideAcked && s.acked)
    const pageOf = new Map(hosts.map((h) => [h.name, h.page]))
    const shown = [...all.values()].filter((h) => Object.values(h.statuses).some(matches))
    shown.sort((a, b) => colorRank[b.worst] - colorRank[a.worst] || a.name.localeCompare(b.name))

    const byPage = new Map<string, typeof shown>()
    shown.forEach((h) => {
      const p = pageOf.get(h.name) || ""
      byPage.set(p, [...(byPage.get(p) ?? []), h])
    })
    const groups: MatrixGroup[] = [...byPage.entries()]
      .sort(([a, ha], [b, hb]) => colorRank[hb[0].worst] - colorRank[ha[0].worst] || a.localeCompare(b))
      .map(([p, hs]) => ({
        key: p || "_",
        title: p ? p.split("/").map((_, i, parts) => titles[parts.slice(0, i + 1).join("/")] || parts[i]).join(" / ") : "Hosts without a page",
        hosts: hs,
      }))
    const counts: Partial<Record<Color, number>> = {}
    shown.forEach((h) => Object.values(h.statuses).filter(matches).forEach((s) => (counts[s.color] = (counts[s.color] ?? 0) + 1)))
    return { groups, matches, worst: worstOf(shown.map((h) => h.worst), "green"), hosts: shown.length, counts }
  }, [hosts, statuses, titles, colors, hideAcked])

  if (l1 || l2 || !view) return <Page><Skeleton className="h-24" /><Skeleton className="h-96" /></Page>

  const toggle = (c: Color) => setColors(colors.includes(c) ? colors.filter((x) => x !== c) : [...colors, c])
  // Only columns with at least one matching (non-green) status in the group.
  const columnFilter = (test: string, hs: { statuses: Record<string, Status> }[]) => hs.some((h) => h.statuses[test] && view.matches(h.statuses[test]))

  return (
    <Page className="max-w-screen-xl">
      <div className={cn("flex flex-col gap-3 rounded-xl border px-5 py-4 sm:flex-row sm:items-center", bannerTint[view.worst])}>
        <LegacyIcon color={view.worst} size="lg" recent />
        <div className="flex-1">
          <h2 className="text-xl font-semibold tracking-tight">All non-green systems</h2>
          <p className="text-muted-foreground text-sm">
            {view.hosts === 0 ? "Nothing to show" : `${view.hosts} host${view.hosts === 1 ? "" : "s"} · `}
            {CHOICES.filter((c) => view.counts[c]).map((c) => `${view.counts[c]} ${colorLabel[c].toLowerCase()}`).join(" · ")}
          </p>
        </div>
        <div className="text-muted-foreground text-xs">{fmtDate(Date.now())}</div>
      </div>

      <Card className="py-3">
        <CardContent className="flex flex-col gap-3 px-4 sm:flex-row sm:items-center">
          <span className="text-muted-foreground text-xs font-medium">Show</span>
          <div className="flex flex-wrap gap-1.5">
            {CHOICES.map((c) => (
              <button
                key={c}
                onClick={() => toggle(c)}
                className={cn("flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs transition-colors", colors.includes(c) ? "bg-accent border-ring" : "opacity-60 hover:opacity-100")}
              >
                <LegacyIcon color={c} />{colorLabel[c]}
              </button>
            ))}
          </div>
          <label className="flex items-center gap-2 text-xs sm:ml-auto">
            <Switch checked={hideAcked} onCheckedChange={setHideAcked} /> Hide acknowledged
          </label>
        </CardContent>
      </Card>

      {view.groups.length === 0 ? (
        <Empty icon={<PartyPopper className="text-status-green size-8" />} title="Everything is green">No checks match the selected colors.</Empty>
      ) : (
        <>
          <LegacyMatrix groups={view.groups} columnFilter={columnFilter} />
          <Legend />
        </>
      )}
    </Page>
  )
}
