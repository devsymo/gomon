import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"

/** Subscribes to server-sent status events and refreshes cached queries. */
export function useLiveEvents() {
  const qc = useQueryClient()
  const [connected, setConnected] = useState(false)
  useEffect(() => {
    let es: EventSource | null = null
    let timer: ReturnType<typeof setTimeout> | undefined
    let retry: ReturnType<typeof setTimeout> | undefined
    let closed = false
    const refresh = () => {
      clearTimeout(timer)
      timer = setTimeout(() => {
        qc.invalidateQueries({
          predicate: (q) => !["metrics", "config", "themes", "tls", "me", "authinfo", "client"].includes(String(q.queryKey[0])),
        })
      }, 750)
    }
    const connect = () => {
      es = new EventSource("/api/events")
      es.onopen = () => setConnected(true)
      es.onmessage = refresh
      es.onerror = () => {
        setConnected(false)
        es?.close()
        if (!closed) retry = setTimeout(connect, 5000)
      }
    }
    connect()
    return () => {
      closed = true
      clearTimeout(timer)
      clearTimeout(retry)
      es?.close()
    }
  }, [qc])
  return connected
}
