import { ConnectionManager, type ConnectionStatus, type WebSocketFactory } from './connection'
import type { Message } from './protocol'
import type { SessionPageFrame, SessionPageTerminal } from './session_page'
import type { TerminalDecoration, TerminalHandle, TerminalMarker } from './terminal'

export type LazyWebSocketFactory = WebSocketFactory

export interface LazyWebSocketConnection {
  start(): Promise<void>
  send(json: string): void
  reconnect(): Promise<void>
  onMessage(callback: (msg: Message) => void): void
  onStatusChange(callback: (status: ConnectionStatus) => void): void
}

export interface SessionPageTerminalPort {
  currentSize(): { cols: number; rows: number }
  clear(): void
  write(data: Uint8Array, callback?: () => void): void | Promise<void>
  registerMarker(cursorYOffset?: number): TerminalMarker | undefined
  registerDecoration(options: Parameters<TerminalHandle['registerDecoration']>[0]): TerminalDecoration | undefined
  scrollToLine(line: number): void
}

export function createLazyWebSocketConnection(
  url: string,
  createSocket?: LazyWebSocketFactory,
): LazyWebSocketConnection {
  return new ConnectionManager(url, createSocket)
}

export function createSessionPageTerminalAdapter(terminal: SessionPageTerminalPort): SessionPageTerminal {
  const seqToMarker = new Map<number, TerminalMarker>()

  async function writeFrame(frame: SessionPageFrame): Promise<void> {
    const marker = terminal.registerMarker()
    if (marker) {
      seqToMarker.set(frame.seq, marker)
    }

    await new Promise<void>((resolve) => {
      void terminal.write(frame.data, resolve)
    })
  }

  return {
    async replaceFrames(frames, anchorSeq) {
      terminal.clear()
      seqToMarker.clear()
      for (const frame of frames) {
        await writeFrame(frame)
      }
      if (anchorSeq !== undefined) {
        const line = seqToMarker.get(anchorSeq)?.line
        if (line !== undefined) {
          terminal.scrollToLine(line)
        }
      }
    },
    async appendFrame(frame) {
      await writeFrame(frame)
    },
    scrollToSeq(seq) {
      const line = seqToMarker.get(seq)?.line
      if (line === undefined) {
        return false
      }
      terminal.scrollToLine(line)
      return true
    },
    highlightSeq(seq) {
      const marker = seqToMarker.get(seq)
      if (!marker) {
        return false
      }

      const decoration = terminal.registerDecoration({
        marker,
        anchor: 'left',
        width: Math.max(1, terminal.currentSize().cols),
        height: 1,
      })
      if (!decoration) {
        return false
      }

      decoration.onRender((element) => {
        element.classList.add('session-highlight')
      })
      setTimeout(() => decoration.dispose(), 1200)
      return true
    },
  }
}
