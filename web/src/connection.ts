import type { Message } from './protocol'

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected'
export type WebSocketFactory = (url: string) => WebSocket

export class ConnectionManager {
  private ws: WebSocket | null = null
  private readonly url: string
  private readonly createSocket: WebSocketFactory
  private messageCallback: ((msg: Message) => void) | null = null
  private statusCallback: ((status: ConnectionStatus) => void) | null = null
  private connectPromise: Promise<void> | null = null
  private connected = false

  constructor(url: string, createSocket: WebSocketFactory = (socketURL) => new WebSocket(socketURL)) {
    this.url = url
    this.createSocket = createSocket
  }

  start(): Promise<void> {
    if (this.connected) {
      return Promise.resolve()
    }
    if (this.connectPromise) {
      return this.connectPromise
    }
    this.connectPromise = this.connect()
    return this.connectPromise
  }

  private connect(): Promise<void> {
    this.emitStatus('connecting')
    const ws = this.createSocket(this.url)
    this.ws = ws
    this.connected = false
    return new Promise<void>((resolve, reject) => {
      let settled = false

      const resolveOnce = () => {
        if (settled) {
          return
        }
        settled = true
        this.connectPromise = null
        resolve()
      }

      const rejectOnce = () => {
        if (settled) {
          return
        }
        settled = true
        this.connectPromise = null
        reject(new Error('websocket closed before connecting'))
      }

      ws.onopen = () => {
        if (this.ws !== ws) {
          return
        }
        this.connected = true
        this.emitStatus('connected')
        resolveOnce()
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
        if (this.ws === ws) {
          this.ws = null
          this.connected = false
          this.emitStatus('disconnected')
          rejectOnce()
        }
      }

      ws.onerror = () => {
        // onclose always fires after onerror, so status update happens there
      }
    })
  }

  send(json: string) {
    if (this.ws && this.connected && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(json)
    }
  }

  reconnect(): Promise<void> {
    this.connected = false
    this.connectPromise = null
    this.ws?.close()
    return this.start()
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
