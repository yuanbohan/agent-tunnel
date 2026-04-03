import { describe, expect, it } from 'vitest'
import { createLazyWebSocketConnection, createSessionPageTerminalAdapter, type LazyWebSocketFactory, type SessionPageTerminalPort } from './session_runtime'
import type { SessionPageFrame } from './session_page'
import type { TerminalDecoration } from './terminal'

function frame(seq: number, text: string): SessionPageFrame {
  return {
    seq,
    data: new TextEncoder().encode(text),
  }
}

describe('session runtime helpers', () => {
  it('defers websocket construction until start is called', async () => {
    const events: string[] = []
    type SocketHandlers = {
      onopen: (() => void) | null
      onmessage: ((event: MessageEvent<string>) => void) | null
      onclose: (() => void) | null
    }
    let socketHandlers: SocketHandlers = {
      onopen: null,
      onmessage: null,
      onclose: null,
    }
    let readyState: number = WebSocket.CONNECTING

    const factory: LazyWebSocketFactory = (url) => {
      events.push(`construct:${url}`)
      socketHandlers = { onopen: null, onmessage: null, onclose: null }
      return {
        get readyState() {
          return readyState
        },
        set onopen(handler: (() => void) | null) {
          socketHandlers.onopen = handler
        },
        set onmessage(handler: ((event: MessageEvent<string>) => void) | null) {
          socketHandlers.onmessage = handler
        },
        set onclose(handler: (() => void) | null) {
          socketHandlers.onclose = handler
        },
        send(json: string) {
          events.push(`send:${json}`)
        },
        close() {
          events.push('close')
        },
      } as unknown as WebSocket
    }

    const conn = createLazyWebSocketConnection('ws://example.test/socket', factory)
    conn.send('before-start')
    expect(events).toEqual([])

    const started = conn.start()
    expect(events).toEqual(['construct:ws://example.test/socket'])

    readyState = WebSocket.OPEN
    socketHandlers.onopen?.()
    await started

    conn.send('after-start')
    expect(events).toEqual([
      'construct:ws://example.test/socket',
      'send:after-start',
    ])
  })

  it('anchors session rewrites with terminal markers instead of raw line counting', async () => {
    const events: string[] = []
    let cursorLine = 0
    const markers: Array<{ line: number | undefined }> = []

    const terminal: SessionPageTerminalPort = {
      currentSize() {
        return { cols: 80, rows: 24 }
      },
      clear() {
        events.push('clear')
        cursorLine = 0
      },
      async write(data: Uint8Array, callback?: () => void) {
        events.push(`write:${new TextDecoder().decode(data)}`)
        cursorLine += 7
        callback?.()
      },
      registerMarker() {
        const marker = { line: cursorLine }
        markers.push(marker)
        return marker
      },
      registerDecoration() {
        events.push('decorate')
        return {
          onRender(callback: (element: HTMLElement) => void) {
            const element = {
              classList: {
                add(value: string) {
                  events.push(`class:${value}`)
                },
              },
            } as unknown as HTMLElement
            callback(element)
            return { dispose() {} }
          },
          dispose() {},
        } as TerminalDecoration
      },
      scrollToLine(line: number) {
        events.push(`scroll:${line}`)
      },
    }

    const adapter = createSessionPageTerminalAdapter(terminal)
    await adapter.replaceFrames([frame(1, 'first'), frame(2, 'second')], 1)

    expect(events).toEqual([
      'clear',
      'write:first',
      'write:second',
      'scroll:0',
    ])
    expect(markers).toHaveLength(2)
    expect(adapter.scrollToSeq(2)).toBe(true)
    expect(events[events.length - 1]).toBe('scroll:7')
    expect(adapter.highlightSeq(2)).toBe(true)
    expect(events.slice(-2)).toEqual(['decorate', 'class:session-highlight'])
  })
})
