export type RelayRoute =
  | { kind: 'dashboard' }
  | { kind: 'session'; sessionId: string }

export function parseRelayRoute(pathname: string): RelayRoute {
  if (pathname === '/' || pathname === '') {
    return { kind: 'dashboard' }
  }

  const match = pathname.match(/^\/sessions\/([^/]+)$/)
  if (match) {
    return { kind: 'session', sessionId: decodeURIComponent(match[1]) }
  }

  return { kind: 'dashboard' }
}
