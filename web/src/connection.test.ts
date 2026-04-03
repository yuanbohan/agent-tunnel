import { describe, expect, it } from 'vitest'
import { ConnectionManager, type ConnectionStatus } from './connection'

type SocketHandlers = {
  onopen: (() => void) | null
  onmessage: ((event: MessageEvent<string>) => void) | null
  onclose: (() => void) | null
}

type SocketStub = WebSocket & {
  triggerOpen(): void
  triggerClose(): void
}

function createSocket(events: string[], label: string): SocketStub {
  const handlers: SocketHandlers = {
    onopen: null,
    onmessage: null,
    onclose: null,
  }
  let readyState: number = WebSocket.CONNECTING

  return {
    get readyState() {
      return readyState
    },
    set onopen(handler: (() => void) | null) {
      handlers.onopen = handler
    },
    set onmessage(handler: ((event: MessageEvent<string>) => void) | null) {
      handlers.onmessage = handler
    },
    set onclose(handler: (() => void) | null) {
      handlers.onclose = handler
    },
    send(json: string) {
      events.push(`send:${label}:${json}`)
    },
    close() {
      events.push(`close:${label}`)
      readyState = WebSocket.CLOSING
    },
    triggerOpen() {
      readyState = WebSocket.OPEN
      handlers.onopen?.()
    },
    triggerClose() {
      readyState = WebSocket.CLOSED
      handlers.onclose?.()
    },
  } as unknown as SocketStub
}

describe('ConnectionManager', () => {
  it('reconnects with a fresh socket instead of resolving against the closing one', async () => {
    const events: string[] = []
    const sockets: SocketStub[] = []
    const statuses: ConnectionStatus[] = []

    const conn = new ConnectionManager('ws://example.test/socket', (url) => {
      const socket = createSocket(events, `socket-${sockets.length + 1}`)
      sockets.push(socket)
      events.push(`construct:${url}:${sockets.length}`)
      return socket
    })
    conn.onStatusChange((status) => {
      statuses.push(status)
    })

    const started = conn.start()
    expect(events).toEqual(['construct:ws://example.test/socket:1'])
    sockets[0]!.triggerOpen()
    await started

    const reconnecting = conn.reconnect()
    expect(events).toEqual([
      'construct:ws://example.test/socket:1',
      'close:socket-1',
      'construct:ws://example.test/socket:2',
    ])

    conn.send('before-open')
    expect(events).not.toContain('send:socket-2:before-open')

    sockets[0]!.triggerClose()
    sockets[1]!.triggerOpen()
    await reconnecting

    conn.send('after-open')
    expect(events).toContain('send:socket-2:after-open')
    expect(statuses).toEqual(['connecting', 'connected', 'connecting', 'connected'])
  })

  it('does not send on sockets that are already closing before onclose fires', async () => {
    const events: string[] = []
    const sockets: SocketStub[] = []

    const conn = new ConnectionManager('ws://example.test/socket', (url) => {
      const socket = createSocket(events, `socket-${sockets.length + 1}`)
      sockets.push(socket)
      events.push(`construct:${url}:${sockets.length}`)
      return socket
    })

    const started = conn.start()
    sockets[0]!.triggerOpen()
    await started

    sockets[0]!.close()
    conn.send('ignored-while-closing')

    expect(events).toEqual([
      'construct:ws://example.test/socket:1',
      'close:socket-1',
    ])
  })
})
