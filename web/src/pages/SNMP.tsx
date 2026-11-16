import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Loader2, MoreHorizontal, Network, Pencil, Play, Plus, Plug, Trash2, X } from "lucide-react"
import { toast } from "sonner"
import { api, enc, type SNMPOID, type SNMPTarget } from "@/lib/api"
import { Page, PageHeader, Empty, Field } from "@/components/Page"
import { ConfirmDialog } from "@/components/StatusActions"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Checkbox } from "@/components/ui/checkbox"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"

const blank: SNMPTarget = {
  id: 0, host: "", address: "", port: 161, version: "2c", community: "public",
  v3: { username: "", securityLevel: "authPriv", authProtocol: "SHA256", authPassword: "", privProtocol: "AES", privPassword: "", contextName: "" },
  intervalSeconds: 300, pollInterfaces: true, oids: [], enabled: true,
}

function OIDEditor({ oids, onChange }: { oids: SNMPOID[]; onChange: (o: SNMPOID[]) => void }) {
  const set = (i: number, patch: Partial<SNMPOID>) => onChange(oids.map((o, j) => (j === i ? { ...o, ...patch } : o)))
  const num = (s: string) => (s.trim() === "" ? null : Number(s))
  return (
    <div className="space-y-2">
      {oids.length > 0 && (
        <div className="text-muted-foreground hidden grid-cols-[1fr_1.4fr_0.8fr_0.6fr_0.6fr_auto] gap-2 text-xs sm:grid">
          <span>Name</span><span>OID</span><span>Column</span><span>Warn</span><span>Crit</span><span className="w-[120px]" />
        </div>
      )}
      {oids.map((o, i) => (
        <div key={i} className="grid grid-cols-2 gap-2 sm:grid-cols-[1fr_1.4fr_0.8fr_0.6fr_0.6fr_auto]">
          <Input placeholder="temperature" value={o.name} onChange={(e) => set(i, { name: e.target.value })} />
          <Input placeholder=".1.3.6.1.4.1…" className="font-mono text-xs" value={o.oid} onChange={(e) => set(i, { oid: e.target.value })} />
          <Input placeholder="snmp" value={o.test ?? ""} onChange={(e) => set(i, { test: e.target.value })} />
          <Input placeholder="—" value={o.warn ?? ""} onChange={(e) => set(i, { warn: num(e.target.value) })} />
          <Input placeholder="—" value={o.crit ?? ""} onChange={(e) => set(i, { crit: num(e.target.value) })} />
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-1 text-xs"><Checkbox checked={o.below} onCheckedChange={(v) => set(i, { below: !!v })} />below</label>
            <label className="flex items-center gap-1 text-xs"><Checkbox checked={o.rate} onCheckedChange={(v) => set(i, { rate: !!v })} />rate</label>
            <Button type="button" variant="ghost" size="icon" className="size-7" onClick={() => onChange(oids.filter((_, j) => j !== i))}><X /></Button>
          </div>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...oids, { name: "", oid: "", rate: false, below: false, test: "" }])}>
        <Plus />Add OID
      </Button>
      <p className="text-muted-foreground text-xs">
        Values are graphed and compared to the thresholds (≥ warn/crit, or ≤ with "below"). "rate" turns counters into per-second rates. Values in another column create a separate status.
      </p>
    </div>
  )
}

function TargetDialog({ open, onOpenChange, initial }: { open: boolean; onOpenChange: (o: boolean) => void; initial: SNMPTarget }) {
  const [t, setT] = useState<SNMPTarget>(initial)
  useEffect(() => setT(initial), [initial])
  const qc = useQueryClient()
  const set = (patch: Partial<SNMPTarget>) => setT((x) => ({ ...x, ...patch }))
  const setV3 = (patch: Partial<SNMPTarget["v3"]>) => setT((x) => ({ ...x, v3: { ...x.v3, ...patch } }))
  const save = useMutation({
    mutationFn: () => (t.id ? api.put(`/snmp/${t.id}`, t) : api.post("/snmp", t)),
    onSuccess: () => { toast.success("SNMP target saved, polling now"); qc.invalidateQueries({ queryKey: ["snmp"] }); onOpenChange(false) },
    onError: (e: Error) => toast.error(e.message),
  })
  const test = useMutation({
    mutationFn: () => api.post<{ ok: boolean; error?: string; system?: { name: string; descr: string; uptime: string } }>("/snmp/test", t),
    onSuccess: (r) => (r.ok ? toast.success(`${r.system?.name || "Device"} responded`, { description: `${r.system?.descr ?? ""} · up ${r.system?.uptime}` }) : toast.error(r.error)),
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t.id ? "Edit SNMP target" : "Add SNMP target"}</DialogTitle>
          <DialogDescription>Polls system info, the interface table and custom OIDs, reported as the snmp and if columns.</DialogDescription>
        </DialogHeader>
        <form className="space-y-5" onSubmit={(e) => { e.preventDefault(); save.mutate() }}>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Host" hint="Created if it does not exist"><Input value={t.host} onChange={(e) => set({ host: e.target.value })} placeholder="switch01" required /></Field>
            <Field label="Address"><Input value={t.address} onChange={(e) => set({ address: e.target.value })} placeholder="192.0.2.2 (default: host)" /></Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="Port"><Input type="number" value={t.port} onChange={(e) => set({ port: Number(e.target.value) })} /></Field>
              <Field label="Version">
                <Select value={t.version} onValueChange={(v) => set({ version: v as SNMPTarget["version"] })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent><SelectItem value="1">v1</SelectItem><SelectItem value="2c">v2c</SelectItem><SelectItem value="3">v3</SelectItem></SelectContent>
                </Select>
              </Field>
            </div>
          </div>
          {t.version !== "3" ? (
            <Field label="Community"><Input type="password" value={t.community ?? ""} onChange={(e) => set({ community: e.target.value })} autoComplete="off" /></Field>
          ) : (
            <div className="grid gap-4 rounded-md border p-4 sm:grid-cols-3">
              <Field label="Username"><Input value={t.v3.username} onChange={(e) => setV3({ username: e.target.value })} /></Field>
              <Field label="Security level">
                <Select value={t.v3.securityLevel} onValueChange={(v) => setV3({ securityLevel: v })}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="noAuthNoPriv">noAuthNoPriv</SelectItem><SelectItem value="authNoPriv">authNoPriv</SelectItem><SelectItem value="authPriv">authPriv</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="Context"><Input value={t.v3.contextName ?? ""} onChange={(e) => setV3({ contextName: e.target.value })} /></Field>
              {t.v3.securityLevel !== "noAuthNoPriv" && (
                <>
                  <Field label="Auth protocol">
                    <Select value={t.v3.authProtocol} onValueChange={(v) => setV3({ authProtocol: v })}>
                      <SelectTrigger><SelectValue /></SelectTrigger>
                      <SelectContent>{["MD5", "SHA", "SHA224", "SHA256", "SHA384", "SHA512"].map((p) => <SelectItem key={p} value={p}>{p}</SelectItem>)}</SelectContent>
                    </Select>
                  </Field>
                  <Field label="Auth password" className="sm:col-span-2"><Input type="password" value={t.v3.authPassword ?? ""} onChange={(e) => setV3({ authPassword: e.target.value })} autoComplete="off" /></Field>
                </>
              )}
              {t.v3.securityLevel === "authPriv" && (
                <>
                  <Field label="Privacy protocol">
                    <Select value={t.v3.privProtocol} onValueChange={(v) => setV3({ privProtocol: v })}>
                      <SelectTrigger><SelectValue /></SelectTrigger>
                      <SelectContent>{["DES", "AES", "AES192", "AES256", "AES192C", "AES256C"].map((p) => <SelectItem key={p} value={p}>{p}</SelectItem>)}</SelectContent>
                    </Select>
                  </Field>
                  <Field label="Privacy password" className="sm:col-span-2"><Input type="password" value={t.v3.privPassword ?? ""} onChange={(e) => setV3({ privPassword: e.target.value })} autoComplete="off" /></Field>
                </>
              )}
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Interval (seconds)"><Input type="number" min={30} value={t.intervalSeconds} onChange={(e) => set({ intervalSeconds: Number(e.target.value) })} /></Field>
            <label className="flex items-center gap-2 pt-6 text-sm"><Switch checked={t.pollInterfaces} onCheckedChange={(v) => set({ pollInterfaces: v })} />Poll interfaces (IF-MIB)</label>
            <label className="flex items-center gap-2 pt-6 text-sm"><Switch checked={t.enabled} onCheckedChange={(v) => set({ enabled: v })} />Enabled</label>
          </div>
          <div>
            <div className="mb-2 text-sm font-medium">Custom OIDs</div>
            <OIDEditor oids={t.oids} onChange={(oids) => set({ oids })} />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => test.mutate()} disabled={test.isPending || !t.host}>
              {test.isPending ? <Loader2 className="animate-spin" /> : <Plug />}Test connection
            </Button>
            <Button type="submit" disabled={save.isPending}>Save</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export default function SNMP() {
  const [params, setParams] = useSearchParams()
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({ queryKey: ["snmp"], queryFn: () => api.get<SNMPTarget[]>("/snmp") })
  const [editing, setEditing] = useState<SNMPTarget | null>(null)
  const [deleting, setDeleting] = useState<SNMPTarget | null>(null)
  useEffect(() => {
    const h = params.get("host")
    if (h) {
      setEditing({ ...blank, host: h })
      setParams({}, { replace: true })
    }
  }, [params, setParams])
  const poll = useMutation({
    mutationFn: (id: number) => api.post(`/snmp/${id}/poll`),
    onSuccess: () => toast.success("Polled"),
    onError: (e: Error) => toast.error(e.message),
  })
  const del = useMutation({
    mutationFn: (id: number) => api.del(`/snmp/${id}`),
    onSuccess: () => { toast.success("Target removed"); qc.invalidateQueries({ queryKey: ["snmp"] }) },
  })
  return (
    <Page>
      <PageHeader title="SNMP" description="Native SNMP v1/v2c/v3 polling — no devmon or external collector needed" actions={<Button onClick={() => setEditing({ ...blank })}><Plus />Add target</Button>} />
      <Card className="py-0">
        <CardContent className="px-0">
          {isLoading ? (
            <div className="space-y-2 p-4">{Array.from({ length: 4 }).map((_, i) => <Skeleton key={i} className="h-8" />)}</div>
          ) : !data?.length ? (
            <Empty icon={<Network className="size-8" />} title="No SNMP targets">Add switches, routers, UPSes, printers… anything that speaks SNMP.</Empty>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Host</TableHead>
                  <TableHead>Address</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead>Interval</TableHead>
                  <TableHead>Polls</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead className="pr-4" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.map((t) => (
                  <TableRow key={t.id}>
                    <TableCell className="pl-4 font-medium"><Link className="hover:underline" to={`/hosts/${enc(t.host)}`}>{t.host}</Link></TableCell>
                    <TableCell className="font-mono text-xs">{t.address}:{t.port}</TableCell>
                    <TableCell><Badge variant="secondary">v{t.version}</Badge></TableCell>
                    <TableCell>{t.intervalSeconds}s</TableCell>
                    <TableCell className="text-muted-foreground text-xs">system{t.pollInterfaces && ", interfaces"}{t.oids.length > 0 && `, ${t.oids.length} OIDs`}</TableCell>
                    <TableCell>{t.enabled ? <Badge variant="outline">enabled</Badge> : <Badge variant="secondary">paused</Badge>}</TableCell>
                    <TableCell className="pr-4 text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild><Button variant="ghost" size="icon"><MoreHorizontal /></Button></DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => poll.mutate(t.id)}><Play />Poll now</DropdownMenuItem>
                          <DropdownMenuItem onClick={() => setEditing(t)}><Pencil />Edit</DropdownMenuItem>
                          <DropdownMenuItem variant="destructive" onClick={() => setDeleting(t)}><Trash2 />Delete</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
      {editing && <TargetDialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)} initial={editing} />}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete SNMP target for ${deleting?.host}?`}
        description="Polling stops; the existing snmp/if statuses go stale (purple) unless dropped."
        confirm="Delete"
        destructive
        onConfirm={() => deleting && del.mutate(deleting.id)}
      />
    </Page>
  )
}
