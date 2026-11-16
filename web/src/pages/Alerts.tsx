import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Bell, Mail, MessageSquare, Pencil, Plus, Send, Trash2, Webhook } from "lucide-react"
import { toast } from "sonner"
import { api, type AlertChannel, type AlertRule } from "@/lib/api"
import { Page, PageHeader, Empty, Field } from "@/components/Page"
import { StatusDot } from "@/components/status"
import { ConfirmDialog } from "@/components/StatusActions"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

const channelTypes: Record<AlertChannel["type"], { label: string; icon: typeof Bell; fields: { key: string; label: string; placeholder?: string; secret?: boolean }[] }> = {
  webhook: { label: "Webhook (JSON)", icon: Webhook, fields: [{ key: "url", label: "URL", placeholder: "https://example.com/hook" }, { key: "authorization", label: "Authorization header", secret: true, placeholder: "Bearer …" }] },
  slack: { label: "Slack / Mattermost", icon: MessageSquare, fields: [{ key: "url", label: "Incoming webhook URL", placeholder: "https://hooks.slack.com/services/…" }] },
  discord: { label: "Discord", icon: MessageSquare, fields: [{ key: "url", label: "Webhook URL", placeholder: "https://discord.com/api/webhooks/…" }] },
  ntfy: { label: "ntfy", icon: Bell, fields: [{ key: "url", label: "Topic URL", placeholder: "https://ntfy.sh/my-alerts" }, { key: "token", label: "Access token", secret: true }] },
  email: {
    label: "Email (SMTP)", icon: Mail,
    fields: [
      { key: "host", label: "SMTP host", placeholder: "smtp.example.com" }, { key: "port", label: "Port", placeholder: "587" },
      { key: "username", label: "Username" }, { key: "password", label: "Password", secret: true },
      { key: "from", label: "From", placeholder: "gomon@example.com" }, { key: "to", label: "To (comma separated)", placeholder: "ops@example.com" },
      { key: "tls", label: "TLS mode", placeholder: "starttls (default) or tls" },
    ],
  },
}

function ChannelDialog({ initial, onClose }: { initial: AlertChannel; onClose: () => void }) {
  const [c, setC] = useState(initial)
  useEffect(() => setC(initial), [initial])
  const qc = useQueryClient()
  const save = useMutation({
    mutationFn: () => (c.id ? api.put(`/alerts/channels/${c.id}`, c) : api.post("/alerts/channels", c)),
    onSuccess: () => { toast.success("Channel saved"); qc.invalidateQueries({ queryKey: ["channels"] }); onClose() },
    onError: (e: Error) => toast.error(e.message),
  })
  const def = channelTypes[c.type]
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader><DialogTitle>{c.id ? "Edit channel" : "New notification channel"}</DialogTitle></DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate() }}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name"><Input value={c.name} onChange={(e) => setC({ ...c, name: e.target.value })} placeholder="On-call" required /></Field>
            <Field label="Type">
              <Select value={c.type} onValueChange={(v) => setC({ ...c, type: v as AlertChannel["type"], config: {} })} disabled={!!c.id}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>{Object.entries(channelTypes).map(([k, v]) => <SelectItem key={k} value={k}>{v.label}</SelectItem>)}</SelectContent>
              </Select>
            </Field>
          </div>
          {def.fields.map((f) => (
            <Field key={f.key} label={f.label}>
              <Input type={f.secret ? "password" : "text"} autoComplete="off" placeholder={f.placeholder} value={c.config[f.key] ?? ""} onChange={(e) => setC({ ...c, config: { ...c.config, [f.key]: e.target.value } })} />
            </Field>
          ))}
          <label className="flex items-center gap-2 text-sm"><Switch checked={c.enabled} onCheckedChange={(v) => setC({ ...c, enabled: v })} />Enabled</label>
          <DialogFooter><Button type="submit" disabled={save.isPending}>Save</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

const blankRule: AlertRule = { id: 0, name: "", hostPattern: "", testPattern: "", excludeHosts: "", excludeTests: "", minColor: "red", delayMinutes: 5, repeatMinutes: 60, notifyRecovery: true, channelIds: [], enabled: true }

function RuleDialog({ initial, channels, onClose }: { initial: AlertRule; channels: AlertChannel[]; onClose: () => void }) {
  const [r, setR] = useState(initial)
  useEffect(() => setR(initial), [initial])
  const qc = useQueryClient()
  const save = useMutation({
    mutationFn: () => (r.id ? api.put(`/alerts/rules/${r.id}`, r) : api.post("/alerts/rules", r)),
    onSuccess: () => { toast.success("Rule saved"); qc.invalidateQueries({ queryKey: ["rules"] }); onClose() },
    onError: (e: Error) => toast.error(e.message),
  })
  const set = (p: Partial<AlertRule>) => setR((x) => ({ ...x, ...p }))
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{r.id ? "Edit alert rule" : "New alert rule"}</DialogTitle>
          <DialogDescription>Patterns are regular expressions; leave empty to match everything. Acknowledged and disabled checks never alert.</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); save.mutate() }}>
          <Field label="Name"><Input value={r.name} onChange={(e) => set({ name: e.target.value })} placeholder="Production critical" required /></Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Hosts"><Input className="font-mono" value={r.hostPattern} onChange={(e) => set({ hostPattern: e.target.value })} placeholder="^prod-" /></Field>
            <Field label="Checks"><Input className="font-mono" value={r.testPattern} onChange={(e) => set({ testPattern: e.target.value })} placeholder="^(conn|http|disk)$" /></Field>
            <Field label="Except hosts"><Input className="font-mono" value={r.excludeHosts} onChange={(e) => set({ excludeHosts: e.target.value })} /></Field>
            <Field label="Except checks"><Input className="font-mono" value={r.excludeTests} onChange={(e) => set({ excludeTests: e.target.value })} placeholder="^msgs$" /></Field>
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Alert on">
              <Select value={r.minColor} onValueChange={(v) => set({ minColor: v as AlertRule["minColor"] })}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="red">Red + purple</SelectItem>
                  <SelectItem value="yellow">Yellow, red + purple</SelectItem>
                  <SelectItem value="purple">Purple (stale) only</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label="Delay (minutes)" hint="Only alert if still bad after"><Input type="number" min={0} value={r.delayMinutes} onChange={(e) => set({ delayMinutes: Number(e.target.value) })} /></Field>
            <Field label="Repeat (minutes)" hint="0 = notify once"><Input type="number" min={0} value={r.repeatMinutes} onChange={(e) => set({ repeatMinutes: Number(e.target.value) })} /></Field>
          </div>
          <Field label="Send to">
            {channels.length === 0 ? (
              <p className="text-muted-foreground text-sm">Create a channel first.</p>
            ) : (
              <div className="grid gap-2 sm:grid-cols-2">
                {channels.map((c) => (
                  <label key={c.id} className="flex items-center gap-2 text-sm">
                    <Checkbox checked={r.channelIds.includes(c.id)} onCheckedChange={(v) => set({ channelIds: v ? [...r.channelIds, c.id] : r.channelIds.filter((x) => x !== c.id) })} />
                    {c.name} <span className="text-muted-foreground text-xs">({c.type})</span>
                  </label>
                ))}
              </div>
            )}
          </Field>
          <div className="flex flex-wrap gap-6">
            <label className="flex items-center gap-2 text-sm"><Switch checked={r.notifyRecovery} onCheckedChange={(v) => set({ notifyRecovery: v })} />Notify on recovery</label>
            <label className="flex items-center gap-2 text-sm"><Switch checked={r.enabled} onCheckedChange={(v) => set({ enabled: v })} />Enabled</label>
          </div>
          <DialogFooter><Button type="submit" disabled={save.isPending}>Save rule</Button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export default function Alerts() {
  const qc = useQueryClient()
  const { data: channels = [] } = useQuery({ queryKey: ["channels"], queryFn: () => api.get<AlertChannel[]>("/alerts/channels") })
  const { data: rules = [] } = useQuery({ queryKey: ["rules"], queryFn: () => api.get<AlertRule[]>("/alerts/rules") })
  const [editChannel, setEditChannel] = useState<AlertChannel | null>(null)
  const [editRule, setEditRule] = useState<AlertRule | null>(null)
  const [confirm, setConfirm] = useState<{ title: string; run: () => void } | null>(null)
  const test = useMutation({
    mutationFn: (id: number) => api.post<{ ok: boolean; error?: string }>(`/alerts/channels/${id}/test`),
    onSuccess: (r) => (r.ok ? toast.success("Test notification sent") : toast.error(r.error)),
  })
  const delChannel = useMutation({ mutationFn: (id: number) => api.del(`/alerts/channels/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["channels"] }) })
  const delRule = useMutation({ mutationFn: (id: number) => api.del(`/alerts/rules/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["rules"] }) })
  const chName = (id: number) => channels.find((c) => c.id === id)?.name ?? `#${id}`

  return (
    <Page>
      <PageHeader title="Alerts" description="Rules decide which problems notify which channels" />
      <div className="grid gap-6 xl:grid-cols-5">
        <Card className="xl:col-span-3">
          <CardHeader>
            <div><CardTitle>Rules</CardTitle><CardDescription>Evaluated continuously against every check</CardDescription></div>
            <Button size="sm" onClick={() => setEditRule({ ...blankRule, channelIds: channels.slice(0, 1).map((c) => c.id) })}><Plus />Rule</Button>
          </CardHeader>
          <CardContent className="space-y-2">
            {rules.length === 0 ? (
              <Empty icon={<Bell className="size-8" />} title="No alert rules">Without rules gomon still shows problems, it just doesn't notify anyone.</Empty>
            ) : rules.map((r) => (
              <div key={r.id} className="flex items-start gap-3 rounded-lg border p-3">
                <StatusDot color={r.minColor === "yellow" ? "yellow" : r.minColor === "purple" ? "purple" : "red"} className="mt-1.5" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 font-medium">{r.name}{!r.enabled && <Badge variant="secondary">disabled</Badge>}</div>
                  <div className="text-muted-foreground mt-1 font-mono text-xs">
                    hosts {r.hostPattern || "*"} · checks {r.testPattern || "*"}{r.excludeHosts && ` · not ${r.excludeHosts}`}{r.excludeTests && ` · not ${r.excludeTests}`}
                  </div>
                  <div className="text-muted-foreground mt-1 text-xs">
                    after {r.delayMinutes}m · {r.repeatMinutes ? `repeat every ${r.repeatMinutes}m` : "once"}{r.notifyRecovery && " · recovery"} → {r.channelIds.map(chName).join(", ") || "no channels"}
                  </div>
                </div>
                <Button variant="ghost" size="icon" onClick={() => setEditRule(r)}><Pencil /></Button>
                <Button variant="ghost" size="icon" onClick={() => setConfirm({ title: `Delete rule "${r.name}"?`, run: () => delRule.mutate(r.id) })}><Trash2 /></Button>
              </div>
            ))}
          </CardContent>
        </Card>
        <Card className="xl:col-span-2">
          <CardHeader>
            <div><CardTitle>Channels</CardTitle><CardDescription>Where notifications are delivered</CardDescription></div>
            <Button size="sm" variant="outline" onClick={() => setEditChannel({ id: 0, name: "", type: "slack", config: {}, enabled: true })}><Plus />Channel</Button>
          </CardHeader>
          <CardContent className="space-y-2">
            {channels.length === 0 ? (
              <Empty title="No channels">Slack, Discord, ntfy, email or any JSON webhook.</Empty>
            ) : channels.map((c) => {
              const Icon = channelTypes[c.type]?.icon ?? Bell
              return (
                <div key={c.id} className="flex items-center gap-3 rounded-lg border p-3">
                  <Icon className="text-muted-foreground size-4" />
                  <div className="min-w-0 flex-1">
                    <div className="font-medium">{c.name}</div>
                    <div className="text-muted-foreground text-xs">{channelTypes[c.type]?.label}{!c.enabled && " · disabled"}</div>
                  </div>
                  <Button variant="ghost" size="icon" title="Send test" onClick={() => test.mutate(c.id)}><Send /></Button>
                  <Button variant="ghost" size="icon" onClick={() => setEditChannel(c)}><Pencil /></Button>
                  <Button variant="ghost" size="icon" onClick={() => setConfirm({ title: `Delete channel "${c.name}"?`, run: () => delChannel.mutate(c.id) })}><Trash2 /></Button>
                </div>
              )
            })}
          </CardContent>
        </Card>
      </div>
      {editChannel && <ChannelDialog initial={editChannel} onClose={() => setEditChannel(null)} />}
      {editRule && <RuleDialog initial={editRule} channels={channels} onClose={() => setEditRule(null)} />}
      <ConfirmDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)} title={confirm?.title ?? ""} description="This cannot be undone." confirm="Delete" destructive onConfirm={() => confirm?.run()} />
    </Page>
  )
}
