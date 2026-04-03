import type { RelaySession } from './types'

export async function fetchSessions(): Promise<RelaySession[]> {
  const response = await fetch('/api/sessions', { credentials: 'same-origin' })
  if (!response.ok) {
    throw new Error(`failed to load sessions: ${response.status}`)
  }
  return response.json() as Promise<RelaySession[]>
}

export function relaySessionWebSocketURL(location: Location, sessionId: string): string {
  const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${location.host}/api/sessions/${encodeURIComponent(sessionId)}/ws`
}
