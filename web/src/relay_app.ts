import './relay.css'
import { ConnectionManager, type ConnectionStatus } from './connection'
import { decodeOutput, encodeInput, type Message } from './protocol'
import { fetchSessions, relaySessionWebSocketURL } from './relay_api'
import { renderSessionCard } from './relay_dashboard'
import { parseRelayRoute } from './relay_routes'
import { nextInputState, stateChipClass, stateChipLabel } from './relay_session_page'
import { createTerminal } from './terminal'

const root = document.getElementById('relay-root')!
const route = parseRelayRoute(window.location.pathname)

if (route.kind === 'dashboard') {
  void renderDashboard()
} else {
  renderSession(route.sessionId)
}

async function renderDashboard() {
  root.innerHTML = `
    <main class="relay-shell">
      <header class="relay-shell__header">
        <div>
          <p class="relay-shell__eyebrow">agent-tunnel relay</p>
          <h1 class="relay-shell__title">Live sessions</h1>
        </div>
      </header>
      <section id="relay-list" class="relay-list">
        <div class="relay-placeholder">Loading sessions…</div>
      </section>
    </main>
  `

  const list = document.getElementById('relay-list')!

  try {
    const sessions = await fetchSessions()
    if (sessions.length === 0) {
      list.innerHTML = `<div class="relay-placeholder">No live sessions right now.</div>`
      return
    }
    list.innerHTML = sessions.map(renderSessionCard).join('')
  } catch (error) {
    list.innerHTML = `<div class="relay-placeholder">Failed to load sessions.</div>`
    console.error(error)
  }
}

function renderSession(sessionId: string) {
  root.innerHTML = `
    <main class="relay-shell relay-shell--session">
      <header class="session-header">
        <a class="back-link" href="/">Live</a>
        <button id="input-chip" class="${stateChipClass(false)}">${stateChipLabel(false)}</button>
      </header>
      <section id="terminal" class="relay-terminal"></section>
    </main>
  `

  const terminal = createTerminal(document.getElementById('terminal')!)
  const conn = new ConnectionManager(relaySessionWebSocketURL(window.location, sessionId))
  const inputChip = document.getElementById('input-chip') as HTMLButtonElement
  let inputEnabled = false

  inputChip.addEventListener('click', () => {
    inputEnabled = nextInputState(inputEnabled)
    inputChip.textContent = stateChipLabel(inputEnabled)
    inputChip.className = stateChipClass(inputEnabled)
  })

  terminal.onData((value) => {
    if (inputEnabled) {
      conn.send(encodeInput(value))
    }
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
    if (status !== 'connected') {
      return
    }
    const { cols, rows } = terminal.currentSize()
    conn.send(JSON.stringify({ type: 'resize', cols, rows }))
  })
}
