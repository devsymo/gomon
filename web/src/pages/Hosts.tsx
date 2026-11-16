import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Download, FileUp, Loader2, Plus, Search, Server } from "lucide-react"
import { toast } from "sonner"
import { api, enc, type Color, type HostSummary } from "@/lib/api"
import { COLORS, colorRank, timeAgo } from "@/lib/format"
import { Page, PageHeader, Empty } from "@/components/Page"
import { StatusDot } from "@/components/status"
import { HostForm, emptyHost, type HostFields } from "@/components/HostForm"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"

function AddHostDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [v, setV] = useState<HostFields>(emptyHost)
  const qc = useQueryClient()
  const navigate = useNavigate()
  const m = useMutation({
    mutationFn: () => api.post("/hosts", v),
    onSuccess: () => {
      toast.success(`Host ${v.name} added`)
      qc.invalidateQueries({ queryKey: ["hosts"] })
      onOpenChange(false)
      navigate(`/hosts/${enc(v.name.toLowerCase())}`)
      setV(emptyHost)
    },
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add host</DialogTitle>
          <DialogDescription>Hosts reporting with a Xymon client are added automatically. Add one here to run network tests against it.</DialogDescription>
        </DialogHeader>
        <form onSubmit={(e) => { e.preventDefault(); m.mutate() }} className="space-y-6">
          <HostForm value={v} onChange={setV} isNew />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" disabled={m.isPending || !v.name}>{m.isPending && <Loader2 className="animate-spin" />}Add host</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

interface ImportResult { created: number; updated: number; warnings: string[]; dryRun: boolean }

function ImportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const [content, setContent] = useState("")
  const [preview, setPreview] = useState<ImportResult | null>(null)
  const qc = useQueryClient()
  const run = useMutation({
    mutationFn: (dryRun: boolean) => api.post<ImportResult>("/hostscfg/import", { content, dryRun }),
    onSuccess: (r) => {
      if (r.dryRun) {
        setPreview(r)
        return
      }
      toast.success(`Imported: ${r.created} created, ${r.updated} updated`)
      qc.invalidateQueries({ queryKey: ["hosts"] })
      onOpenChange(false)
      setContent("")
      setPreview(null)
    },
    onError: (e: Error) => toast.error(e.message),
  })
  const onFile = async (f: File | undefined) => {
    if (!f) return
    setContent(await f.text())
    setPreview(null)
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Import hosts.cfg</DialogTitle>
          <DialogDescription>Paste your Xymon hosts.cfg (include files pasted inline). Existing hosts get their IP, tags and page updated.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <Input type="file" accept=".cfg,.txt,text/plain" onChange={(e) => onFile(e.target.files?.[0])} />
          <Textarea
            value={content}
            onChange={(e) => { setContent(e.target.value); setPreview(null) }}
            rows={14}
            className="font-mono text-xs"
            placeholder={"page servers Servers\n192.0.2.10   web01.example.com   # conn ssh https://web01.example.com/\n0.0.0.0      db01.example.com    # conn pgsql"}
          />
          {preview && (
            <div className="bg-muted rounded-md p-3 text-sm">
              Will create <b>{preview.created}</b> and update <b>{preview.updated}</b> hosts.
              {preview.warnings.length > 0 && (
                <ul className="text-muted-foreground mt-2 list-disc pl-5 text-xs">
                  {preview.warnings.slice(0, 20).map((w) => <li key={w}>{w}</li>)}
                </ul>
              )}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => run.mutate(true)} disabled={!content || run.isPending}>Preview</Button>
          <Button onClick={() => run.mutate(false)} disabled={!content || run.isPending}>{run.isPending && <Loader2 className="animate-spin" />}Import</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function Hosts() {
  const { data: hosts, isLoading } = useQuery({ queryKey: ["hosts"], queryFn: () => api.get<HostSummary[]>("/hosts") })
  const [q, setQ] = useState("")
  const [color, setColor] = useState<"all" | Color>("all")
  const [addOpen, setAddOpen] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const rows = useMemo(() => {
    const s = q.trim().toLowerCase()
    return (hosts ?? [])
      .filter((h) => (color === "all" || h.color === color) && (!s || [h.name, h.ip, h.os, h.page, h.description, h.tags].some((x) => x?.toLowerCase().includes(s))))
      .sort((a, b) => colorRank[b.color] - colorRank[a.color] || a.name.localeCompare(b.name))
  }, [hosts, q, color])

  return (
    <Page>
      <PageHeader
        title="Hosts"
        description="Everything that reports to gomon or is tested by it"
        actions={
          <>
            <Button variant="outline" asChild><a href="/api/hostscfg"><Download />Export hosts.cfg</a></Button>
            <Button variant="outline" onClick={() => setImportOpen(true)}><FileUp />Import</Button>
            <Button onClick={() => setAddOpen(true)}><Plus />Add host</Button>
          </>
        }
      />
      <Card className="py-0">
        <CardContent className="px-0">
          <div className="flex flex-col gap-2 border-b p-3 sm:flex-row">
            <div className="relative flex-1">
              <Search className="text-muted-foreground absolute top-2.5 left-2.5 size-4" />
              <Input placeholder="Search name, IP, OS, page, tags…" className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
            <Select value={color} onValueChange={(v) => setColor(v as Color | "all")}>
              <SelectTrigger className="sm:w-40"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All statuses</SelectItem>
                {COLORS.map((c) => <SelectItem key={c} value={c}><span className="capitalize">{c}</span></SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          {isLoading ? (
            <div className="space-y-2 p-4">{Array.from({ length: 6 }).map((_, i) => <Skeleton key={i} className="h-8" />)}</div>
          ) : rows.length === 0 ? (
            <Empty icon={<Server className="size-8" />} title={hosts?.length ? "No hosts match" : "No hosts yet"}>
              {!hosts?.length && "Point a Xymon client at this server, add a host, or import your hosts.cfg."}
            </Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Host</TableHead>
                  <TableHead>IP</TableHead>
                  <TableHead>OS</TableHead>
                  <TableHead>Page</TableHead>
                  <TableHead>Checks</TableHead>
                  <TableHead className="pr-4 text-right">Last report</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((h) => (
                  <TableRow key={h.name}>
                    <TableCell className="pl-4">
                      <Link to={`/hosts/${enc(h.name)}`} className="flex items-center gap-2 font-medium hover:underline">
                        <StatusDot color={h.color} pulse />
                        {h.name}
                      </Link>
                      {h.description && <div className="text-muted-foreground ml-[18px] text-xs">{h.description}</div>}
                    </TableCell>
                    <TableCell className="text-muted-foreground font-mono text-xs">{h.ip || "—"}</TableCell>
                    <TableCell>{h.os ? <Badge variant="secondary">{h.os}</Badge> : <span className="text-muted-foreground">—</span>}</TableCell>
                    <TableCell className="text-muted-foreground">{h.page || "—"}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        {COLORS.filter((c) => h.counts[c]).map((c) => (
                          <span key={c} className="flex items-center gap-1 text-xs tabular-nums"><StatusDot color={c} className="size-2" />{h.counts[c]}</span>
                        ))}
                        {Object.keys(h.tests).length === 0 && <span className="text-muted-foreground text-xs">none</span>}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground pr-4 text-right text-xs">{timeAgo(h.lastSeen)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      <AddHostDialog open={addOpen} onOpenChange={setAddOpen} />
      <ImportDialog open={importOpen} onOpenChange={setImportOpen} />
    </Page>
  )
}
