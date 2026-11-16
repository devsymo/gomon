import { useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Check, Copy, Download, KeyRound, Loader2, Lock, Moon, Monitor, Plus, ShieldCheck, Sun, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { api, type User } from "@/lib/api"
import { useTheme, type Mode } from "@/lib/theme"
import { duration, fmtDate, timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Page, PageHeader, Field, CodeBlock } from "@/components/Page"
import { ConfirmDialog } from "@/components/StatusActions"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Badge } from "@/components/ui/badge"

function copy(text: string) {
  navigator.clipboard?.writeText(text)
  toast.success("Copied to clipboard")
}

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/x-pem-file" }))
  const a = document.createElement("a")
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

function Appearance() {
  const { themes, themeId, setThemeId, mode, setMode, resolvedDark } = useTheme()
  const modes: { v: Mode; l: string; i: typeof Sun }[] = [{ v: "light", l: "Light", i: Sun }, { v: "dark", l: "Dark", i: Moon }, { v: "system", l: "System", i: Monitor }]
  return (
    <div className="space-y-6">
      <Card>
        <CardHeader><div><CardTitle>Mode</CardTitle><CardDescription>Stored in this browser</CardDescription></div></CardHeader>
        <CardContent className="flex gap-2">
          {modes.map((m) => (
            <Button key={m.v} variant={mode === m.v ? "default" : "outline"} onClick={() => setMode(m.v)}><m.i />{m.l}</Button>
          ))}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><div><CardTitle>Theme</CardTitle><CardDescription>Built-in themes from qui</CardDescription></div></CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {themes.map((t) => {
            const v = resolvedDark ? t.dark : t.light
            return (
              <button key={t.id} onClick={() => setThemeId(t.id)} className={cn("hover:border-ring rounded-lg border p-3 text-left transition-colors", themeId === t.id && "border-ring ring-ring/40 ring-2")}>
                <div className="mb-3 flex h-14 overflow-hidden rounded-md border" style={{ background: v["--background"] }}>
                  <div className="w-1/4" style={{ background: v["--sidebar"] ?? v["--card"] }} />
                  <div className="flex flex-1 items-center gap-1.5 p-2">
                    <span className="h-3 w-10 rounded" style={{ background: v["--primary"] }} />
                    <span className="h-3 w-6 rounded" style={{ background: v["--accent"] }} />
                    <span className="h-3 w-6 rounded" style={{ background: v["--muted"] }} />
                  </div>
                </div>
                <div className="flex items-center justify-between text-sm font-medium">{t.name}{themeId === t.id && <Check className="size-4" />}</div>
                <div className="text-muted-foreground line-clamp-2 text-xs">{t.description}</div>
              </button>
            )
          })}
        </CardContent>
      </Card>
    </div>
  )
}

interface TLSInfo {
  caPem: string; caSubject: string; caExpires: string; caFingerprint: string; plainListen: string; tlsListen: string
  requireClientCert: boolean; customServerCert: boolean; serverNames: string[]
}

function Agents() {
  const { data: info } = useQuery({ queryKey: ["tls"], queryFn: () => api.get<TLSInfo>("/tls") })
  const [hostname, setHostname] = useState("")
  const [issued, setIssued] = useState<{ host: string; cert: string; key: string } | null>(null)
  const [csr, setCsr] = useState("")
  const [signed, setSigned] = useState("")
  const issue = useMutation({
    mutationFn: () => api.post<{ cert: string; key: string }>("/tls/issue", { hostname, days: 365 }),
    onSuccess: (r) => setIssued({ host: hostname.toLowerCase(), ...r }),
    onError: (e: Error) => toast.error(e.message),
  })
  const sign = useMutation({
    mutationFn: () => api.post<{ cert: string }>("/tls/sign", { csr, days: 365 }),
    onSuccess: (r) => setSigned(r.cert),
    onError: (e: Error) => toast.error(e.message),
  })
  const server = window.location.hostname
  const tlsPort = info?.tlsListen.split(":").pop() || "1985"
  const h = issued?.host || "HOSTNAME"
  const clientCfg = `# xymonclient.cfg (Xymon 4.3.32+ built with OpenSSL)
XYMSRV="xymons://${server}"
XYMONDTLSPORT="${tlsPort}"
XYMON_TLS_CA="/etc/xymon/gomon-ca.pem"
XYMON_TLS_CERT="/etc/xymon/${h}.pem"
XYMON_TLS_KEY="/etc/xymon/${h}.key"`
  const relay = `# Older clients / plugins without TLS: run the relay on the host
# and set XYMSRV="127.0.0.1" — everything is forwarded encrypted.
gomon relay --listen 127.0.0.1:1984 \\
  --server xymons://${server}:${tlsPort} \\
  --ca /etc/gomon/ca.pem --cert /etc/gomon/${h}.pem --key /etc/gomon/${h}.key`

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <div>
            <CardTitle className="flex items-center gap-2"><ShieldCheck className="text-status-green size-5" />Encrypted agent communication</CardTitle>
            <CardDescription>Xymon protocol over TLS, compatible with upstream xymon's xymons:// recipients and per-host client certificates</CardDescription>
          </div>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Field label="Plaintext listener"><div className="font-mono text-sm">{info?.plainListen || "disabled"}</div></Field>
          <Field label="TLS listener"><div className="flex items-center gap-1.5 font-mono text-sm"><Lock className="size-3.5" />{info?.tlsListen || "disabled"}</div></Field>
          <Field label="Client certificates"><div className="text-sm">{info?.requireClientCert ? <Badge>required</Badge> : <Badge variant="outline">optional</Badge>}</div></Field>
          <Field label="Server certificate"><div className="text-sm">{info?.customServerCert ? "custom" : "issued by built-in CA"}</div></Field>
          <Field label="CA fingerprint (SHA-256)" className="sm:col-span-2 lg:col-span-3">
            <div className="text-muted-foreground font-mono text-xs break-all">{info?.caFingerprint.match(/.{2}/g)?.join(":")}</div>
          </Field>
          <div className="flex items-end">
            <Button variant="outline" asChild><a href="/api/tls/ca.pem"><Download />CA certificate</a></Button>
          </div>
          {!info?.customServerCert && (
            <Field label="Server certificate names" className="sm:col-span-2 lg:col-span-4" hint="Clients must connect to one of these; add more with xymon.tlsServerNames">
              <div className="flex flex-wrap gap-1">{info?.serverNames.map((n) => <Badge key={n} variant="secondary" className="font-mono">{n}</Badge>)}</div>
            </Field>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader><div><CardTitle>Issue a client certificate</CardTitle><CardDescription>Proves which host a client may report for (subjectAltName = hostname)</CardDescription></div></CardHeader>
          <CardContent className="space-y-4">
            <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); issue.mutate() }}>
              <Input placeholder="web01.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} required />
              <Button type="submit" disabled={issue.isPending}>{issue.isPending ? <Loader2 className="animate-spin" /> : <KeyRound />}Issue</Button>
            </form>
            {issued && (
              <div className="space-y-2 rounded-md border p-3">
                <div className="text-sm">Certificate for <b>{issued.host}</b>. The private key is shown only once.</div>
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" variant="outline" onClick={() => download(`${issued.host}.pem`, issued.cert)}><Download />{issued.host}.pem</Button>
                  <Button size="sm" variant="outline" onClick={() => download(`${issued.host}.key`, issued.key)}><Download />{issued.host}.key</Button>
                  <Button size="sm" variant="outline" onClick={() => download("gomon-ca.pem", info?.caPem ?? "")}><Download />gomon-ca.pem</Button>
                </div>
              </div>
            )}
            <div className="space-y-2">
              <div className="text-sm font-medium">Or sign a CSR made on the host (key never leaves it)</div>
              <Textarea rows={4} className="font-mono text-xs" placeholder="-----BEGIN CERTIFICATE REQUEST-----" value={csr} onChange={(e) => setCsr(e.target.value)} />
              <Button size="sm" variant="outline" onClick={() => sign.mutate()} disabled={!csr || sign.isPending}>Sign CSR</Button>
              {signed && <Button size="sm" variant="outline" className="ml-2" onClick={() => download("client.pem", signed)}><Download />Download certificate</Button>}
              <p className="text-muted-foreground text-xs">
                <code>openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -keyout host.key -out host.csr -subj "/CN=HOSTNAME"</code>
              </p>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><div><CardTitle>Client configuration</CardTitle><CardDescription>Existing agents and ext scripts keep working unchanged</CardDescription></div></CardHeader>
          <CardContent className="space-y-4">
            <div>
              <div className="mb-1 flex items-center justify-between text-sm font-medium">Xymon client with TLS<Button size="icon" variant="ghost" className="size-7" onClick={() => copy(clientCfg)}><Copy /></Button></div>
              <CodeBlock>{clientCfg}</CodeBlock>
            </div>
            <div>
              <div className="mb-1 flex items-center justify-between text-sm font-medium">Any other client: TLS relay<Button size="icon" variant="ghost" className="size-7" onClick={() => copy(relay)}><Copy /></Button></div>
              <CodeBlock>{relay}</CodeBlock>
            </div>
            <div>
              <div className="mb-1 text-sm font-medium">Plaintext (unchanged legacy setup)</div>
              <CodeBlock>{`XYMSRV="${server}"   # port ${info?.plainListen.split(":").pop() || "1984"}`}</CodeBlock>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function ConfigEditor({ name, description }: { name: string; description: string }) {
  const qc = useQueryClient()
  const { data } = useQuery({ queryKey: ["config", name], queryFn: () => api.get<{ content: string }>(`/config/${name}`) })
  const [text, setText] = useState("")
  const [err, setErr] = useState("")
  useEffect(() => { if (data) setText(data.content) }, [data])
  const save = useMutation({
    mutationFn: () => api.put(`/config/${name}`, { content: text }),
    onSuccess: () => { toast.success(`${name} saved and applied`); setErr(""); qc.invalidateQueries({ queryKey: ["config", name] }) },
    onError: (e: Error) => setErr(e.message),
  })
  const dirty = data && text !== data.content
  return (
    <Card>
      <CardHeader>
        <div><CardTitle className="font-mono">{name}</CardTitle><CardDescription>{description}</CardDescription></div>
        <Button onClick={() => save.mutate()} disabled={!dirty || save.isPending}>{save.isPending && <Loader2 className="animate-spin" />}Save</Button>
      </CardHeader>
      <CardContent className="space-y-2">
        <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={18} spellCheck={false} className="font-mono text-xs leading-relaxed [field-sizing:fixed]" />
        {err && <p className="text-destructive text-sm">{err}</p>}
      </CardContent>
    </Card>
  )
}

function APIKeys() {
  const qc = useQueryClient()
  const { data = [] } = useQuery({ queryKey: ["apikeys"], queryFn: () => api.get<{ id: number; name: string; createdAt: string; lastUsed: string | null }[]>("/apikeys") })
  const [name, setName] = useState("")
  const [created, setCreated] = useState("")
  const [del, setDel] = useState<number | null>(null)
  const create = useMutation({
    mutationFn: () => api.post<{ key: string }>("/apikeys", { name }),
    onSuccess: (r) => { setCreated(r.key); setName(""); qc.invalidateQueries({ queryKey: ["apikeys"] }) },
    onError: (e: Error) => toast.error(e.message),
  })
  const remove = useMutation({ mutationFn: (id: number) => api.del(`/apikeys/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["apikeys"] }) })
  return (
    <Card>
      <CardHeader><div><CardTitle>API keys</CardTitle><CardDescription>For scripts and integrations: send as the <code>X-API-Key</code> header</CardDescription></div></CardHeader>
      <CardContent className="space-y-4">
        <form className="flex gap-2" onSubmit={(e) => { e.preventDefault(); create.mutate() }}>
          <Input placeholder="Key name (e.g. grafana)" value={name} onChange={(e) => setName(e.target.value)} required />
          <Button type="submit"><Plus />Create</Button>
        </form>
        {created && (
          <div className="bg-muted flex items-center gap-2 rounded-md p-3">
            <code className="flex-1 text-xs break-all">{created}</code>
            <Button size="icon" variant="ghost" onClick={() => copy(created)}><Copy /></Button>
          </div>
        )}
        <div className="divide-y rounded-md border">
          {data.length === 0 && <div className="text-muted-foreground p-3 text-sm">No API keys.</div>}
          {data.map((k) => (
            <div key={k.id} className="flex items-center gap-3 p-3 text-sm">
              <KeyRound className="text-muted-foreground size-4" />
              <span className="font-medium">{k.name}</span>
              <span className="text-muted-foreground text-xs">created {fmtDate(k.createdAt)} · last used {k.lastUsed ? timeAgo(k.lastUsed) : "never"}</span>
              <Button size="icon" variant="ghost" className="ml-auto" onClick={() => setDel(k.id)}><Trash2 /></Button>
            </div>
          ))}
        </div>
      </CardContent>
      <ConfirmDialog open={del !== null} onOpenChange={(o) => !o && setDel(null)} title="Revoke API key?" description="Anything using it stops working immediately." confirm="Revoke" destructive onConfirm={() => del !== null && remove.mutate(del)} />
    </Card>
  )
}

function Account() {
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: () => api.get<User>("/auth/me") })
  const { data: sys } = useQuery({ queryKey: ["system"], queryFn: () => api.get<{ version: string; protocol: string; uptime: number; oidc: boolean; stats: Record<string, number> }>("/system") })
  const [cur, setCur] = useState("")
  const [next, setNext] = useState("")
  const change = useMutation({
    mutationFn: () => api.post("/auth/password", { currentPassword: cur, newPassword: next }),
    onSuccess: () => { toast.success("Password changed"); setCur(""); setNext("") },
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      <Card>
        <CardHeader><div><CardTitle>Account</CardTitle><CardDescription>Signed in as {me?.username} via {me?.method}</CardDescription></div></CardHeader>
        <CardContent>
          {me?.method === "password" ? (
            <form className="grid gap-4" onSubmit={(e) => { e.preventDefault(); change.mutate() }}>
              <Field label="Current password"><Input type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} required /></Field>
              <Field label="New password" hint="At least 8 characters"><Input type="password" autoComplete="new-password" minLength={8} value={next} onChange={(e) => setNext(e.target.value)} required /></Field>
              <div><Button type="submit" disabled={change.isPending}>Change password</Button></div>
            </form>
          ) : (
            <p className="text-muted-foreground text-sm">Your account is managed by your identity provider.</p>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><div><CardTitle>About</CardTitle><CardDescription>gomon {sys?.version} · Xymon protocol {sys?.protocol}</CardDescription></div></CardHeader>
        <CardContent className="grid grid-cols-2 gap-4 text-sm">
          <Field label="Uptime"><span>{sys ? duration(sys.uptime) : "—"}</span></Field>
          <Field label="Single sign-on"><span>{sys?.oidc ? "OIDC enabled" : "not configured"}</span></Field>
          {sys && Object.entries(sys.stats).map(([k, v]) => (
            <Field key={k} label={k.replace(/([A-Z])/g, " $1").toLowerCase()}><span className="tabular-nums">{v.toLocaleString()}</span></Field>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}

const TABS = ["appearance", "agents", "config", "apikeys", "account"] as const

export default function Settings() {
  const { tab = "appearance" } = useParams()
  const navigate = useNavigate()
  return (
    <Page>
      <PageHeader title="Settings" />
      <Tabs value={TABS.includes(tab as (typeof TABS)[number]) ? tab : "appearance"} onValueChange={(v) => navigate(`/settings/${v}`)}>
        <TabsList>
          <TabsTrigger value="appearance">Appearance</TabsTrigger>
          <TabsTrigger value="agents">Agents &amp; TLS</TabsTrigger>
          <TabsTrigger value="config">Configuration</TabsTrigger>
          <TabsTrigger value="apikeys">API keys</TabsTrigger>
          <TabsTrigger value="account">Account</TabsTrigger>
        </TabsList>
        <TabsContent value="appearance"><Appearance /></TabsContent>
        <TabsContent value="agents"><Agents /></TabsContent>
        <TabsContent value="config" className="space-y-6">
          <ConfigEditor name="analysis.cfg" description="Thresholds for client data (cpu, disk, memory, procs, ports, logs) in Xymon's analysis.cfg format. Applied immediately." />
          <ConfigEditor name="client-local.cfg" description="Returned to Xymon clients with their report: per host, class or OS (e.g. log files to watch)." />
        </TabsContent>
        <TabsContent value="apikeys"><APIKeys /></TabsContent>
        <TabsContent value="account"><Account /></TabsContent>
      </Tabs>
    </Page>
  )
}
