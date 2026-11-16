import { useEffect, useMemo, useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, BellOff, Bell, Lock, LockOpen, MoreHorizontal, Network, Pencil, Play, Radar, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { api, enc, type ClientData, type Host, type SNMPTarget, type Status } from "@/lib/api"
import { colorRank, since, timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Page, Empty, CodeBlock } from "@/components/Page"
import { StatusBadge, StatusDot, statusText } from "@/components/status"
import { HostForm, type HostFields } from "@/components/HostForm"
import { MetricsCharts } from "@/components/MetricsCharts"
import { DisableDialog, ConfirmDialog } from "@/components/StatusActions"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Skeleton } from "@/components/ui/skeleton"
import { Input } from "@/components/ui/input"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"

interface HostResponse { host: Host; statuses: Status[]; snmp: SNMPTarget[] | null }

function StatusGrid({ host, statuses }: { host: string; statuses: Status[] }) {
  const sorted = [...statuses].sort((a, b) => colorRank[b.color] - colorRank[a.color] || a.test.localeCompare(b.test))
  if (sorted.length === 0) {
    return <Empty icon={<Radar className="size-8" />} title="No checks yet">Statuses appear when a client, plugin, network test or SNMP poll reports for this host.</Empty>
  }
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
      {sorted.map((s) => (
        <Link key={s.test} to={`/hosts/${enc(host)}/${enc(s.test)}`} className="group">
          <Card className={cn("hover:border-ring/60 h-full gap-2 py-4 transition-colors", s.disabled && "opacity-70")}>
            <CardHeader className="items-center px-4">
              <div className="flex items-center gap-2 font-medium">
                <StatusDot color={s.color} pulse />
                {s.test}
              </div>
              <div className="flex items-center gap-1.5">
                {s.acked && <Badge variant="outline">acked</Badge>}
                {s.transport.startsWith("TLS") ? <Lock className="text-status-green size-3.5" /> : s.transport === "plaintext" ? <LockOpen className="text-muted-foreground size-3.5" /> : null}
                <span className={cn("text-xs font-medium capitalize", statusText(s.color))}>{s.color}</span>
              </div>
            </CardHeader>
            <CardContent className="px-4">
              <p className="text-muted-foreground line-clamp-2 text-sm" title={s.summary}>{s.summary || "—"}</p>
              <div className="text-muted-foreground mt-2 flex justify-between text-xs">
                <span>{s.color === "green" ? "OK" : s.color} for {since(s.lastChange)}</span>
                <span>updated {timeAgo(s.lastUpdate)}</span>
              </div>
            </CardContent>
          </Card>
        </Link>
      ))}
    </div>
  )
}

function ClientTab({ host }: { host: string }) {
  const { data, isLoading } = useQuery({ queryKey: ["client", host], queryFn: () => api.get<ClientData[]>(`/hosts/${enc(host)}/client`) })
  const [sel, setSel] = useState<string>("")
  const [filter, setFilter] = useState("")
  const sections = useMemo(() => (data ?? []).flatMap((c) => (c.sections ?? []).map((s) => ({ ...s, id: `${c.collector}/${s.name}`, collector: c.collector }))), [data])
  useEffect(() => {
    if (!sel && sections.length) setSel(sections[0].id)
  }, [sections, sel])
  if (isLoading) return <Skeleton className="h-64" />
  if (!data?.length) return <Empty title="No client data">Install the Xymon client (or any compatible agent) on this host and point it at gomon.</Empty>
  const cur = sections.find((s) => s.id === sel)
  const shown = sections.filter((s) => !filter || s.name.includes(filter))
  return (
    <div className="grid gap-4 lg:grid-cols-[220px_1fr]">
      <Card className="gap-2 py-3">
        <CardContent className="space-y-2 px-2">
          <Input placeholder="Filter sections" value={filter} onChange={(e) => setFilter(e.target.value)} className="h-8" />
          <div className="max-h-[60vh] space-y-0.5 overflow-y-auto">
            {shown.map((s) => (
              <button key={s.id} onClick={() => setSel(s.id)} className={cn("w-full truncate rounded px-2 py-1 text-left font-mono text-xs", sel === s.id ? "bg-accent text-accent-foreground" : "hover:bg-accent/50")}>
                {s.collector && <span className="text-muted-foreground">{s.collector}/</span>}[{s.name}]
              </button>
            ))}
          </div>
        </CardContent>
      </Card>
      <Card className="min-w-0 gap-3 py-4">
        <CardHeader className="px-4">
          <CardTitle className="font-mono text-sm">[{cur?.name}]</CardTitle>
          <CardDescription className="mt-0 text-xs">
            {data[0].os} · {data[0].class || "no class"} · from {data[0].sender} via {data[0].transport} · {timeAgo(data[0].time)}
          </CardDescription>
        </CardHeader>
        <CardContent className="px-4">
          <CodeBlock className="max-h-[60vh]">{cur?.content || "(empty)"}</CodeBlock>
        </CardContent>
      </Card>
    </div>
  )
}

function SettingsTab({ host }: { host: Host }) {
  const qc = useQueryClient()
  const [v, setV] = useState<HostFields>(host)
  useEffect(() => setV(host), [host])
  const save = useMutation({
    mutationFn: () => api.put(`/hosts/${enc(host.name)}`, v),
    onSuccess: () => {
      toast.success("Host saved")
      qc.invalidateQueries({ queryKey: ["host", host.name] })
    },
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Host settings</CardTitle>
          <CardDescription>Stored in hosts.cfg format; network tests run every few minutes</CardDescription>
        </div>
      </CardHeader>
      <CardContent>
        <form onSubmit={(e) => { e.preventDefault(); save.mutate() }} className="space-y-6">
          <HostForm value={v} onChange={setV} />
          <div className="flex justify-end">
            <Button type="submit" disabled={save.isPending}>Save changes</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

export default function HostDetail() {
  const { host = "" } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data, isLoading, error } = useQuery({ queryKey: ["host", host], queryFn: () => api.get<HostResponse>(`/hosts/${enc(host)}`) })
  const [tab, setTab] = useState("status")
  const [disableOpen, setDisableOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [renameOpen, setRenameOpen] = useState(false)
  const [newName, setNewName] = useState("")

  const netTest = useMutation({
    mutationFn: () => api.post(`/hosts/${enc(host)}/nettest`),
    onSuccess: () => toast.success("Network tests completed"),
    onError: (e: Error) => toast.error(e.message),
  })
  const enableAll = useMutation({
    mutationFn: () => api.post(`/status/${enc(host)}/*/enable`, { allTests: true }),
    onSuccess: () => { toast.success("All checks enabled"); qc.invalidateQueries() },
  })
  const del = useMutation({
    mutationFn: () => api.del(`/hosts/${enc(host)}`),
    onSuccess: () => { toast.success(`${host} deleted`); qc.invalidateQueries(); navigate("/hosts") },
    onError: (e: Error) => toast.error(e.message),
  })
  const rename = useMutation({
    mutationFn: () => api.post(`/hosts/${enc(host)}/rename`, { name: newName }),
    onSuccess: () => { toast.success("Host renamed"); qc.invalidateQueries(); navigate(`/hosts/${enc(newName.toLowerCase())}`) },
    onError: (e: Error) => toast.error(e.message),
  })

  if (isLoading) return <Page><Skeleton className="h-20" /><Skeleton className="h-64" /></Page>
  if (error || !data) return <Page><Empty title="Host not found"><Link to="/hosts" className="underline">Back to hosts</Link></Empty></Page>

  const h = data.host
  const worst = data.statuses.reduce((w, s) => (colorRank[s.color] > colorRank[w] ? s.color : w), "clear" as Status["color"])
  const anyDisabled = data.statuses.some((s) => s.disabled)

  return (
    <Page>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <Link to="/hosts" className="text-muted-foreground mb-2 inline-flex items-center gap-1 text-sm hover:underline"><ArrowLeft className="size-3.5" />Hosts</Link>
          <div className="flex items-center gap-3">
            <StatusDot color={worst} className="size-3.5" pulse />
            <h2 className="truncate text-2xl font-semibold tracking-tight">{h.name}</h2>
            <StatusBadge color={worst} />
          </div>
          <div className="text-muted-foreground mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
            {h.description && <span>{h.description}</span>}
            <span className="font-mono">{h.ip || "no IP"}</span>
            {h.os && <Badge variant="secondary">{h.os}</Badge>}
            {h.class && h.class !== h.os && <Badge variant="outline">class {h.class}</Badge>}
            {h.page && (
              <Link className="hover:underline" to={`/pages/${h.page.split("/").map(encodeURIComponent).join("/")}`}>
                page: {h.page}{h.group && ` · ${h.group}`}
              </Link>
            )}
          </div>
          {h.tags && <div className="mt-2 font-mono text-xs text-muted-foreground"># {h.tags}</div>}
        </div>
        <div className="flex shrink-0 gap-2">
          <Button variant="outline" onClick={() => netTest.mutate()} disabled={netTest.isPending || !h.tags}>
            <Play className={cn(netTest.isPending && "animate-pulse")} />Run tests
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild><Button variant="outline" size="icon"><MoreHorizontal /></Button></DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => setTab("settings")}><Pencil />Edit host</DropdownMenuItem>
              <DropdownMenuItem onClick={() => { setNewName(h.name); setRenameOpen(true) }}><Pencil />Rename</DropdownMenuItem>
              <DropdownMenuItem onClick={() => navigate(`/snmp?host=${enc(h.name)}`)}><Network />Add SNMP target</DropdownMenuItem>
              <DropdownMenuSeparator />
              {anyDisabled ? (
                <DropdownMenuItem onClick={() => enableAll.mutate()}><Bell />Enable all checks</DropdownMenuItem>
              ) : (
                <DropdownMenuItem onClick={() => setDisableOpen(true)}><BellOff />Disable all checks</DropdownMenuItem>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onClick={() => setDeleteOpen(true)}><Trash2 />Delete host</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {h.notes && (
        <Card className="py-3"><CardContent className="px-4 text-sm whitespace-pre-wrap">{h.notes}</CardContent></Card>
      )}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="status">Checks <span className="text-muted-foreground text-xs">{data.statuses.length}</span></TabsTrigger>
          <TabsTrigger value="graphs">Graphs</TabsTrigger>
          <TabsTrigger value="client">Client data</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="status"><StatusGrid host={h.name} statuses={data.statuses} /></TabsContent>
        <TabsContent value="graphs"><MetricsCharts host={h.name} /></TabsContent>
        <TabsContent value="client"><ClientTab host={h.name} /></TabsContent>
        <TabsContent value="settings"><SettingsTab host={h} /></TabsContent>
      </Tabs>

      <DisableDialog open={disableOpen} onOpenChange={setDisableOpen} host={h.name} test="*" allTests />
      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={`Delete ${h.name}?`}
        description="Removes the host with all its statuses, history, metrics, client data and SNMP targets. A client that keeps reporting will re-register it."
        confirm="Delete host"
        destructive
        onConfirm={() => del.mutate()}
      />
      <Dialog open={renameOpen} onOpenChange={setRenameOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>Rename host</DialogTitle></DialogHeader>
          <Input value={newName} onChange={(e) => setNewName(e.target.value)} />
          <p className="text-muted-foreground text-xs">History and metrics move with the host. Update the client's hostname too, or it will register under the old name again.</p>
          <DialogFooter><Button onClick={() => rename.mutate()} disabled={!newName || newName === h.name}>Rename</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </Page>
  )
}
