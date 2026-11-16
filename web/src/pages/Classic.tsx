import { useMemo } from "react"
import { Link, useParams } from "react-router-dom"
import { useQuery } from "@tanstack/react-query"
import { ChevronRight, FolderTree, LayoutGrid } from "lucide-react"
import { api, type Color, type HostSummary, type Status } from "@/lib/api"
import { colorLabel, fmtDate } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Page, Empty } from "@/components/Page"
import { LegacyIcon, LegacyMatrix, Legend, bannerTint, buildHosts, worstOf, type MatrixGroup, type MatrixHost } from "@/components/LegacyMatrix"
import { Skeleton } from "@/components/ui/skeleton"

const pageURL = (path: string) => "/pages" + (path ? "/" + path.split("/").map(encodeURIComponent).join("/") : "")

interface SubPage { path: string; name: string; title: string; worst: Color; hosts: number; pages: number }

export default function Classic() {
  const params = useParams()
  const path = (params["*"] ?? "").replace(/^\/+|\/+$/g, "")
  const { data: hosts, isLoading: l1 } = useQuery({ queryKey: ["hosts"], queryFn: () => api.get<HostSummary[]>("/hosts") })
  const { data: statuses, isLoading: l2 } = useQuery({ queryKey: ["statuses"], queryFn: () => api.get<Status[]>("/statuses") })
  const { data: titles = {} } = useQuery({ queryKey: ["pages"], queryFn: () => api.get<Record<string, string>>("/pages") })

  const view = useMemo(() => {
    if (!hosts || !statuses) return null
    const all = buildHosts(hosts, statuses)
    const pageOf = new Map(hosts.map((h) => [h.name, h.page.replace(/^\/+|\/+$/g, "")]))
    const inSubtree = (p: string, root: string) => root === "" || p === root || p.startsWith(root + "/")

    // Subpages: the next path segment below the current page.
    const subs = new Map<string, MatrixHost[]>()
    const subPageSets = new Map<string, Set<string>>()
    hosts.forEach((h) => {
      const p = pageOf.get(h.name)!
      if (!inSubtree(p, path) || p === path) return
      const rest = (path ? p.slice(path.length + 1) : p).split("/")
      const child = path ? `${path}/${rest[0]}` : rest[0]
      subs.set(child, [...(subs.get(child) ?? []), all.get(h.name)!])
      if (rest.length > 1) subPageSets.set(child, (subPageSets.get(child) ?? new Set()).add(rest[1]))
    })
    const subpages: SubPage[] = [...subs.entries()]
      .map(([p, hs]) => ({
        path: p, name: p.split("/").pop()!, title: titles[p] || p.split("/").pop()!,
        worst: worstOf(hs.map((h) => h.worst)), hosts: hs.length, pages: subPageSets.get(p)?.size ?? 0,
      }))
      .sort((a, b) => a.title.localeCompare(b.title))

    // Hosts directly on this page, by group heading.
    const direct = hosts.filter((h) => pageOf.get(h.name) === path)
    const byGroup = new Map<string, MatrixHost[]>()
    direct.forEach((h) => byGroup.set(h.group || "", [...(byGroup.get(h.group || "") ?? []), all.get(h.name)!]))
    const groups: MatrixGroup[] = [...byGroup.entries()]
      .sort(([a], [b]) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)))
      .map(([g, hs]) => ({ key: g || "_", title: g, hosts: hs.sort((a, b) => a.name.localeCompare(b.name)) }))

    const subtree = [...all.values()].filter((h) => inSubtree(pageOf.get(h.name) ?? "", path))
    return { subpages, groups, worst: worstOf(subtree.map((h) => h.worst)), exists: subtree.length > 0, total: subtree.length }
  }, [hosts, statuses, titles, path])

  const crumbs = path ? path.split("/") : []
  const title = path ? titles[path] || crumbs[crumbs.length - 1] : "All pages"

  if (l1 || l2 || !view) return <Page><Skeleton className="h-24" /><Skeleton className="h-96" /></Page>

  return (
    <Page className="max-w-screen-xl">
      <div className={cn("flex flex-col gap-3 rounded-xl border px-5 py-4 sm:flex-row sm:items-center", bannerTint[view.worst])}>
        <LegacyIcon color={view.worst} size="lg" recent />
        <div className="min-w-0 flex-1">
          <nav className="text-muted-foreground flex flex-wrap items-center gap-1 text-xs">
            <Link to="/pages" className="hover:underline">Pages</Link>
            {crumbs.map((c, i) => {
              const p = crumbs.slice(0, i + 1).join("/")
              return (
                <span key={p} className="flex items-center gap-1">
                  <ChevronRight className="size-3" />
                  <Link to={pageURL(p)} className="hover:underline">{titles[p] || c}</Link>
                </span>
              )
            })}
          </nav>
          <h2 className="truncate text-xl font-semibold tracking-tight">{title}</h2>
        </div>
        <div className="text-right text-xs">
          <div className="font-medium">{colorLabel[view.worst]}</div>
          <div className="text-muted-foreground">{view.total} hosts · {fmtDate(Date.now())}</div>
        </div>
      </div>

      {!view.exists ? (
        <Empty icon={<LayoutGrid className="size-8" />} title={path ? "No such page" : "No hosts yet"}>
          Pages come from the Page field of each host (e.g. <code>linux</code> or <code>linux/db</code>) or from <code>page</code>/<code>subpage</code>/<code>group</code> lines in an imported hosts.cfg.
        </Empty>
      ) : (
        <>
          {view.subpages.length > 0 && (
            <section>
              <h3 className="text-muted-foreground mb-2 flex items-center gap-1.5 text-xs font-semibold tracking-wider uppercase">
                <FolderTree className="size-3.5" />{path ? "Subpages" : "Pages"}
              </h3>
              <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {view.subpages.map((s) => (
                  <Link key={s.path} to={pageURL(s.path)} className="bg-card hover:border-ring/60 hover:bg-accent/40 flex items-center gap-3 rounded-lg border px-4 py-3 transition-colors">
                    <LegacyIcon color={s.worst} size="lg" recent />
                    <div className="min-w-0">
                      <div className="truncate font-medium">{s.title}</div>
                      <div className="text-muted-foreground text-xs">
                        {s.hosts} host{s.hosts === 1 ? "" : "s"}{s.pages > 0 && ` · ${s.pages} subpage${s.pages === 1 ? "" : "s"}`}
                      </div>
                    </div>
                    <ChevronRight className="text-muted-foreground ml-auto size-4" />
                  </Link>
                ))}
              </div>
            </section>
          )}
          {view.groups.length > 0 && <LegacyMatrix groups={view.groups} />}
          <Legend />
        </>
      )}
    </Page>
  )
}
