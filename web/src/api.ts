import type { HistoryPage, RelaySession, SessionReadState } from './types'

export async function fetchSessions(): Promise<RelaySession[]> {
  const response = await fetch('/api/sessions', { credentials: 'same-origin' })
  if (!response.ok) {
    throw new Error(`failed to load sessions: ${response.status}`)
  }
  return response.json() as Promise<RelaySession[]>
}

export async function fetchSessionHistory(sessionId: string, before?: number, after?: number): Promise<HistoryPage> {
  const url = new URL(`/api/sessions/${encodeURIComponent(sessionId)}/history`, window.location.origin)
  if (before !== undefined) {
    url.searchParams.set('before', String(before))
  }
  if (after !== undefined) {
    url.searchParams.set('after', String(after))
  }

  const response = await fetch(url, { credentials: 'same-origin' })
  if (!response.ok) {
    throw new Error(`failed to load session history: ${response.status}`)
  }
  return response.json() as Promise<HistoryPage>
}

export async function markSessionRead(sessionId: string, seq: number): Promise<SessionReadState> {
  const response = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}/read`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ seq }),
  })
  if (!response.ok) {
    throw new Error(`failed to mark session read: ${response.status}`)
  }
  return response.json() as Promise<SessionReadState>
}

export function relaySessionWebSocketURL(location: Location, sessionId: string): string {
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${location.host}/api/sessions/${encodeURIComponent(sessionId)}/ws`
}
