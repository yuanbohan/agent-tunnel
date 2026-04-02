import './style.css'
import { createTerminal, type DisplayMode } from './terminal'
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

let ptyCols = 0
let ptyRows = 0
let displayMode: DisplayMode = 'scroll'

// Floating toggle button
const toggleBtn = document.createElement('button')
toggleBtn.id = 'wrap-toggle'
toggleBtn.textContent = 'Wrap'
toggleBtn.title = 'Toggle line wrapping'
document.body.appendChild(toggleBtn)

toggleBtn.addEventListener('click', () => {
  displayMode = displayMode === 'scroll' ? 'wrap' : 'scroll'
  toggleBtn.textContent = displayMode === 'scroll' ? 'Wrap' : 'Scroll'
  terminal.setDisplayMode(displayMode, ptyCols, ptyRows)
  termContainer.classList.toggle('scroll-mode', displayMode === 'scroll')
})

// Start in scroll mode
termContainer.classList.add('scroll-mode')

terminal.onData((str) => {
  conn.send(encodeInput(str))
})

conn.onMessage((msg: Message) => {
  if (msg.type === 'output') {
    terminal.write(decodeOutput(msg))
  } else if (msg.type === 'resize') {
    ptyCols = msg.cols
    ptyRows = msg.rows
    if (displayMode === 'scroll') {
      terminal.setDisplayMode('scroll', ptyCols, ptyRows)
    }
  }
})

conn.onStatusChange((status: ConnectionStatus) => {
  statusDot.className = status
  reconnectBtn.hidden = status !== 'disconnected'

  switch (status) {
    case 'connecting':
      statusText.textContent = `Connecting to ${AGENT_URL}…`
      break
    case 'connected':
      statusText.textContent = `Connected to ${AGENT_URL}`
      break
    case 'disconnected':
      statusText.textContent = `Disconnected from ${AGENT_URL}`
      break
  }
})

reconnectBtn.addEventListener('click', () => conn.reconnect())
