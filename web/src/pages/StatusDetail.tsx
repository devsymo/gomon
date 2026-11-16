import { useState } from "react"
import { Link, useNavigate, useParams } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Bell, BellOff, Check, Copy, Lock, LockOpen, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { api, enc, type HistoryEvent, type Status } from "@/lib/api"
import { fmtDate, since, timeAgo } from "@/lib/format"
import { Page, Empty } from "@/components/Page"
import { StatusBadge, StatusDot, StatusMessage } from "@/components/status"
import { MetricsCharts } from "@/components/MetricsCharts"
import { AckDialog, ConfirmDialog, DisableDialog } from "@/components/StatusActions"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

function Meta({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className="mt-0.5 text-sm font-medium">{children}</div>
    </div>
  )
}

export default function StatusDetail() {
  const { host = "", test = "" } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { data: st, isLoading, error } = useQuery({ queryKey: ["status", host, test], queryFn: () => api.get<Status>(`/status/${enc(host)}/${enc(test)}`) })
  const { data: history } = useQuery({ queryKey: ["history", host, test], queryFn: () => api.get<HistoryEvent[]>(`/history?host=${enc(host)}&test=${enc(test)}&limit=50`) })
  const [ackOpen, setAckOpen] = useState(false)
  const [disOpen, setDisOpen] = useState(false)
  const [dropOpen, setDropOpen] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const { data: past } = useQuery({
    queryKey: ["historyEvent", selected],
    queryFn: () => api.get<HistoryEvent>(`/history/${selected}`),
    enabled: selected !== null,
  })
  const enable = useMutation({
    mutationFn: () => api.post(`/status/${enc(host)}/${enc(test)}/enable`),
    onSuccess: () => { toast.success("Enabled"); qc.invalidateQueries() },
  })
  const drop = useMutation({
    mutationFn: () => api.del(`/status/${enc(host)}/${enc(test)}`),
    onSuccess: () => { toast.success("Status dropped"); qc.invalidateQueries(); navigate(`/hosts/${enc(host)}`) },
  })

  if (isLoading) return <Page><Skeleton className="h-24" /><Skeleton className="h-96" /></Page>
  if (error || !st) return <Page><Empty title="Status not found"><Link className="underline" to={`/hosts/${enc(host)}`}>Back to {host}</Link></Empty></Page>

  const tls = st.transport.startsWith("TLS")
  const shown = selected !== null && past ? past : null

  return (
    <Page>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <Link to={`/hosts/${enc(host)}`} className="text-muted-foreground mb-2 inline-flex items-center gap-1 text-sm hover:underline"><ArrowLeft className="size-3.5" />{host}</Link>
          <div className="flex items-center gap-3">
            <StatusDot color={st.color} className="size-3.5" pulse />
            <h2 className="truncate text-2xl font-semibold tracking-tight">{host}<span className="text-muted-foreground">.</span>{test}</h2>
            <StatusBadge color={st.color} />
          </div>
          <p className="text-muted-foreground mt-2 text-sm">{st.summary}</p>
        </div>
        <div className="flex shrink-0 flex-wrap gap-2">
          {(st.color === "red" || st.color === "yellow" || st.color === "purple") && !st.acked && (
            <Button onClick={() => setAckOpen(true)}><Check />Acknowledge</Button>
          )}
          {st.disabled ? (
            <Button variant="outline" onClick={() => enable.mutate()}><Bell />Enable</Button>
          ) : (
            <Button variant="outline" onClick={() => setDisOpen(true)}><BellOff />Disable</Button>
          )}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="outline" size="icon" onClick={() => setDropOpen(true)}><Trash2 /></Button>
            </TooltipTrigger>
            <TooltipContent>Drop this status</TooltipContent>
          </Tooltip>
        </div>
      </div>

      {st.acked && (
        <Card className="border-status-blue/40 py-3"><CardContent className="px-4 text-sm">
          <b>Acknowledged</b> by {st.ackBy} until {fmtDate(st.ackUntil)}{st.ackMsg && <>: {st.ackMsg}</>}
        </CardContent></Card>
      )}
      {st.disabled && (
        <Card className="border-status-blue/40 py-3"><CardContent className="px-4 text-sm">
          <b>Disabled</b> {st.disabledUntil && new Date(st.disabledUntil).getFullYear() >= 9999 ? "until the check is OK" : `until ${fmtDate(st.disabledUntil)}`}{st.disableMsg && <>: {st.disableMsg}</>}
        </CardContent></Card>
      )}

      <Card className="py-4">
        <CardContent className="grid grid-cols-2 gap-4 px-4 sm:grid-cols-3 lg:grid-cols-6">
          <Meta label="In this state for">{since(st.lastChange)}</Meta>
          <Meta label="Last update">{timeAgo(st.lastUpdate)}</Meta>
          <Meta label="Valid until">{fmtDate(st.validUntil)}</Meta>
          <Meta label="Sender"><span className="font-mono">{st.sender || "—"}</span></Meta>
          <Meta label="Transport">
            <span className="flex items-center gap-1.5">
              {tls ? <Lock className="text-status-green size-3.5" /> : <LockOpen className="text-muted-foreground size-3.5" />}
              {st.transport || "—"}
            </span>
          </Meta>
          <Meta label="Cookie">
            {st.cookie ? (
              <button className="flex items-center gap-1 font-mono" onClick={() => { navigator.clipboard?.writeText(String(st.cookie)); toast.success("Cookie copied") }}>
                {st.cookie}<Copy className="size-3" />
              </button>
            ) : "—"}
          </Meta>
        </CardContent>
      </Card>

      <div className="grid gap-6 xl:grid-cols-[1fr_320px]">
        <Card className="min-w-0">
          <CardHeader>
            <div>
              <CardTitle>{shown ? "Status at " + fmtDate(shown.time) : "Current status"}</CardTitle>
              {shown && <CardDescription>Historical snapshot · <button className="underline" onClick={() => setSelected(null)}>show current</button></CardDescription>}
            </div>
          </CardHeader>
          <CardContent>
            <div className="bg-muted/40 rounded-md border p-4">
              <StatusMessage text={(shown ? shown.message : st.message) || st.summary} />
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <div>
              <CardTitle>History</CardTitle>
              <CardDescription>Color changes for this check</CardDescription>
            </div>
          </CardHeader>
          <CardContent className="px-2">
            {!history?.length ? (
              <p className="text-muted-foreground px-3 text-sm">No changes recorded.</p>
            ) : (
              <ul className="max-h-[480px] space-y-0.5 overflow-y-auto">
                {history.map((e) => (
                  <li key={e.id}>
                    <button
                      onClick={() => setSelected(e.id === selected ? null : e.id)}
                      className={`hover:bg-accent flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left text-sm ${selected === e.id ? "bg-accent" : ""}`}
                    >
                      <StatusDot color={e.color} />
                      <span className="capitalize">{e.color}</span>
                      {e.color === "purple" && <Badge variant="outline" className="text-[10px]">stale</Badge>}
                      <span className="text-muted-foreground ml-auto text-xs">{timeAgo(e.time)}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <MetricsCharts host={host} test={test} compact />

      <AckDialog open={ackOpen} onOpenChange={setAckOpen} host={host} test={test} />
      <DisableDialog open={disOpen} onOpenChange={setDisOpen} host={host} test={test} />
      <ConfirmDialog open={dropOpen} onOpenChange={setDropOpen} title={`Drop ${host}.${test}?`} description="Removes the current status. It reappears if the check reports again." confirm="Drop" destructive onConfirm={() => drop.mutate()} />
    </Page>
  )
}
