import './style.css'
import { createTerminal } from './terminal'
import { ConnectionManager, type ConnectionStatus } from './connection'
import { encodeInput, decodeOutput } from './protocol'
import type { Message } from './protocol'
import { sessionWebSocketURL } from './session_url'

const AGENT_URL = sessionWebSocketURL(window.location)

const statusDot = document.getElementById('status-dot')!
const statusText = document.getElementById('status-text')!
const reconnectBtn = document.getElementById('reconnect-btn') as HTMLButtonElement

const termContainer = document.getElementById('terminal')!
const terminal = createTerminal(termContainer)
const conn = new ConnectionManager(AGENT_URL)

terminal.onData((str) => {
  conn.send(encodeInput(str))
})

terminal.onResize((cols, rows) => {
  conn.send(JSON.stringify({ type: 'resize', cols, rows }))
})

conn.onMessage((msg: Message) => {
  if (msg.type === 'output') {
    terminal.write(decodeOutput(msg))
  }
})

conn.onStatusChange((status: ConnectionStatus) => {
  statusDot.className = status
  reconnectBtn.hidden = status !== 'disconnected'

  switch (status) {
    case 'connecting':
      statusText.textContent = `Connecting to ${AGENT_URL}...`
      break
    case 'connected': {
      statusText.textContent = `Connected to ${AGENT_URL}`
      const { cols, rows } = terminal.currentSize()
      conn.send(JSON.stringify({ type: 'resize', cols, rows }))
      break
    }
    case 'disconnected':
      statusText.textContent = `Disconnected from ${AGENT_URL}`
      break
  }
})

reconnectBtn.addEventListener('click', () => conn.reconnect())
