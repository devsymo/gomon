import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, enc } from "@/lib/api"
import { Field } from "@/components/Page"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

const DURATIONS = [
  { v: "30", l: "30 minutes" },
  { v: "60", l: "1 hour" },
  { v: "240", l: "4 hours" },
  { v: "480", l: "8 hours" },
  { v: "1440", l: "1 day" },
  { v: "10080", l: "1 week" },
]

export function AckDialog({ open, onOpenChange, host, test }: { open: boolean; onOpenChange: (o: boolean) => void; host: string; test: string }) {
  const [minutes, setMinutes] = useState("60")
  const [message, setMessage] = useState("")
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: () => api.post(`/status/${enc(host)}/${enc(test)}/ack`, { minutes: Number(minutes), message }),
    onSuccess: () => { toast.success("Acknowledged"); qc.invalidateQueries(); onOpenChange(false); setMessage("") },
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Acknowledge {host}.{test}</DialogTitle>
          <DialogDescription>Silences alerts for this problem. The ack is cleared automatically when the check recovers.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <Field label="For">
            <Select value={minutes} onValueChange={setMinutes}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>{DURATIONS.map((d) => <SelectItem key={d.v} value={d.v}>{d.l}</SelectItem>)}</SelectContent>
            </Select>
          </Field>
          <Field label="Message"><Input value={message} onChange={(e) => setMessage(e.target.value)} placeholder="Working on it" autoFocus /></Field>
        </div>
        <DialogFooter><Button onClick={() => m.mutate()} disabled={m.isPending}>Acknowledge</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function DisableDialog({ open, onOpenChange, host, test, allTests }: { open: boolean; onOpenChange: (o: boolean) => void; host: string; test: string; allTests?: boolean }) {
  const [minutes, setMinutes] = useState("60")
  const [message, setMessage] = useState("")
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: () => api.post(`/status/${enc(host)}/${enc(test)}/disable`, { minutes: Number(minutes), message, allTests }),
    onSuccess: () => { toast.success("Disabled"); qc.invalidateQueries(); onOpenChange(false); setMessage("") },
    onError: (e: Error) => toast.error(e.message),
  })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Disable {allTests ? `all checks on ${host}` : `${host}.${test}`}</DialogTitle>
          <DialogDescription>Disabled checks show blue and never alert — use for maintenance.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <Field label="For">
            <Select value={minutes} onValueChange={setMinutes}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {DURATIONS.map((d) => <SelectItem key={d.v} value={d.v}>{d.l}</SelectItem>)}
                <SelectItem value="-1">Until it is OK again</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Reason"><Input value={message} onChange={(e) => setMessage(e.target.value)} placeholder="Planned maintenance" autoFocus /></Field>
        </div>
        <DialogFooter><Button onClick={() => m.mutate()} disabled={m.isPending}>Disable</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ConfirmDialog({
  open, onOpenChange, title, description, confirm, destructive, onConfirm,
}: { open: boolean; onOpenChange: (o: boolean) => void; title: string; description: string; confirm: string; destructive?: boolean; onConfirm: () => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant={destructive ? "destructive" : "default"} onClick={() => { onConfirm(); onOpenChange(false) }}>{confirm}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
