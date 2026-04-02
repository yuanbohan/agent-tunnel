export interface BrowserLocationLike {
  protocol: string
  host: string
}

export function sessionWebSocketURL(location: BrowserLocationLike): string {
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${location.host}/ws`
}
