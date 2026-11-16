import { useState } from "react"
import type { Host } from "@/lib/api"
import { Field } from "@/components/Page"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

export type HostFields = Pick<Host, "name" | "ip" | "tags" | "description" | "page" | "class" | "notes">

export const emptyHost: HostFields = { name: "", ip: "", tags: "", description: "", page: "", class: "", notes: "" }

export function useHostForm(initial?: Partial<HostFields>) {
  return useState<HostFields>({ ...emptyHost, ...initial })
}

export function HostForm({ value, onChange, isNew }: { value: HostFields; onChange: (v: HostFields) => void; isNew?: boolean }) {
  const set = (k: keyof HostFields) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => onChange({ ...value, [k]: e.target.value })
  return (
    <div className="grid gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Hostname" hint={isNew ? "As the Xymon client reports it (MACHINEDOTS)" : undefined}>
          <Input value={value.name} onChange={set("name")} disabled={!isNew} placeholder="web01.example.com" required />
        </Field>
        <Field label="IP address" hint="0.0.0.0 or empty resolves the hostname">
          <Input value={value.ip} onChange={set("ip")} placeholder="192.0.2.10" />
        </Field>
      </div>
      <Field
        label="Network tests (hosts.cfg tags)"
        hint={
          <>
            Space separated, as in Xymon's hosts.cfg: <code>conn</code> (ping), services like <code>ssh</code> <code>smtp</code> <code>imaps</code> <code>ssh:2222</code>,
            <code> !telnet</code> (must be closed), URLs <code>https://example.com/</code>, <code>cont;URL;regex</code>, <code>httpstatus;URL;200|301</code>, <code>noconn</code>.
          </>
        }
      >
        <Input value={value.tags} onChange={set("tags")} placeholder="conn ssh https://web01.example.com/" className="font-mono" />
      </Field>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Page / group" hint="Groups hosts on the dashboard (page or page/subpage)">
          <Input value={value.page} onChange={set("page")} placeholder="linux" />
        </Field>
        <Field label="Client class" hint="Overrides the class sent by the client (analysis.cfg CLASS=)">
          <Input value={value.class} onChange={set("class")} placeholder="" />
        </Field>
      </div>
      <Field label="Description">
        <Input value={value.description} onChange={set("description")} placeholder="Primary web server" />
      </Field>
      <Field label="Notes">
        <Textarea value={value.notes} onChange={set("notes")} rows={3} placeholder="Runbook links, owners, …" />
      </Field>
    </div>
  )
}
