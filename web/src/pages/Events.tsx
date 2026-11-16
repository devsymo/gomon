import { useState } from "react"
import { Link } from "react-router-dom"
import { useInfiniteQuery } from "@tanstack/react-query"
import { History, Loader2, Search } from "lucide-react"
import { api, enc, type Color, type HistoryEvent } from "@/lib/api"
import { COLORS, fmtDate, timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Page, PageHeader, Empty } from "@/components/Page"
import { StatusDot } from "@/components/status"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Skeleton } from "@/components/ui/skeleton"

export default function Events() {
  const [host, setHost] = useState("")
  const [test, setTest] = useState("")
  const [colors, setColors] = useState<Color[]>([])
  const q = useInfiniteQuery({
    queryKey: ["events", host, test, colors.join(",")],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api.get<HistoryEvent[]>(`/history?limit=100&host=${enc(host.trim())}&test=${enc(test.trim())}&colors=${colors.join(",")}${pageParam ? `&before=${pageParam}` : ""}`),
    getNextPageParam: (last) => (last.length === 100 ? last[last.length - 1].id : undefined),
  })
  const rows = q.data?.pages.flat() ?? []
  const toggle = (c: Color) => setColors((cs) => (cs.includes(c) ? cs.filter((x) => x !== c) : [...cs, c]))

  return (
    <Page>
      <PageHeader title="Events" description="Every status color change, newest first" />
      <Card className="py-0">
        <CardContent className="px-0">
          <div className="flex flex-col gap-2 border-b p-3 lg:flex-row lg:items-center">
            <div className="relative lg:w-64">
              <Search className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
              <Input className="pl-8" placeholder="Exact host name" value={host} onChange={(e) => setHost(e.target.value)} />
            </div>
            <Input className="lg:w-48" placeholder="Check (e.g. cpu)" value={test} onChange={(e) => setTest(e.target.value)} />
            <div className="flex flex-wrap gap-1.5 lg:ml-auto">
              {COLORS.map((c) => (
                <button
                  key={c}
                  onClick={() => toggle(c)}
                  className={cn("flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs capitalize transition-colors", colors.includes(c) ? "bg-accent border-ring" : "hover:bg-accent/50")}
                >
                  <StatusDot color={c} className="size-2" />{c}
                </button>
              ))}
            </div>
          </div>
          {q.isLoading ? (
            <div className="space-y-2 p-4">{Array.from({ length: 8 }).map((_, i) => <Skeleton key={i} className="h-8" />)}</div>
          ) : rows.length === 0 ? (
            <Empty icon={<History className="size-8" />} title="No events" />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Time</TableHead>
                  <TableHead>Change</TableHead>
                  <TableHead>Host</TableHead>
                  <TableHead>Check</TableHead>
                  <TableHead className="w-full pr-4">Summary</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className="text-muted-foreground pl-4 text-xs" title={fmtDate(e.time)}>{timeAgo(e.time)}</TableCell>
                    <TableCell>
                      <span className="flex items-center gap-1.5 text-xs capitalize">
                        <StatusDot color={e.prevColor} className="size-2 opacity-60" />→<StatusDot color={e.color} className="size-2.5" />{e.color}
                      </span>
                    </TableCell>
                    <TableCell className="font-medium"><Link className="hover:underline" to={`/hosts/${enc(e.host)}`}>{e.host}</Link></TableCell>
                    <TableCell><Link className="hover:underline" to={`/hosts/${enc(e.host)}/${enc(e.test)}`}>{e.test}</Link></TableCell>
                    <TableCell className="text-muted-foreground max-w-0 truncate pr-4" title={e.summary}>{e.summary}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {q.hasNextPage && (
            <div className="flex justify-center border-t p-3">
              <Button variant="outline" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>
                {q.isFetchingNextPage && <Loader2 className="animate-spin" />}Load more
              </Button>
            </div>
          )}
        </CardContent>
      </Card>
    </Page>
  )
}
