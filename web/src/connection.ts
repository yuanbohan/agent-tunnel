import type { Message } from './protocol'

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected'

export class ConnectionManager {
  private ws: WebSocket | null = null
  private readonly url: string
  private messageCallback: ((msg: Message) => void) | null = null
  private statusCallback: ((status: ConnectionStatus) => void) | null = null

  constructor(url: string) {
    this.url = url
    this.connect()
  }

  private connect() {
    this.emitStatus('connecting')
    const ws = new WebSocket(this.url)
    this.ws = ws

    ws.onopen = () => {
      this.emitStatus('connected')
    }

    ws.onmessage = (event: MessageEvent<string>) => {
      try {
        const msg = JSON.parse(event.data) as Message
        this.messageCallback?.(msg)
      } catch {
        // ignore malformed frames
      }
    }

    ws.onclose = () => {
      this.ws = null
      this.emitStatus('disconnected')
    }

    ws.onerror = () => {
      // onclose always fires after onerror, so status update happens there
    }
  }

  send(json: string) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(json)
    }
  }

  reconnect() {
    this.ws?.close()
    this.connect()
  }

  onMessage(callback: (msg: Message) => void) {
    this.messageCallback = callback
  }

  onStatusChange(callback: (status: ConnectionStatus) => void) {
    this.statusCallback = callback
  }

  private emitStatus(status: ConnectionStatus) {
    this.statusCallback?.(status)
  }
}
