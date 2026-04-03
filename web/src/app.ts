import './style.css'
import { encodeInput, type Message } from './protocol'
import { fetchSessionHistory, fetchSessions, markSessionRead, relaySessionWebSocketURL } from './api'
import { renderSessionCard } from './dashboard'
import { mountDashboardPreviews } from './dashboard_preview'
import { startDashboardView } from './dashboard_view'
import { parseRelayRoute } from './routes'
import {
  createSessionPageController,
  nextInputState,
  stateChipClass,
  stateChipLabel,
  nextDisplayMode,
  displayModeChipClass,
  displayModeChipLabel,
} from './session_page'
import { createLazyWebSocketConnection, createSessionPageTerminalAdapter } from './session_runtime'
import { isTerminalAutoResponse } from './input_filter'
import { createTerminal, type DisplayMode } from './terminal'

const root = document.getElementById('relay-root')!
const route = parseRelayRoute(window.location.pathname)

if (route.kind === 'dashboard') {
  renderDashboard()
} else {
  renderSession(route.sessionId)
}

function renderDashboard() {
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
  startDashboardView({
    root: list,
    fetchSessions,
    renderSessionCard,
    mountDashboardPreviews,
    intervalMs: 5000,
    onError: (error) => {
      console.error(error)
    },
  })
}

function renderSession(sessionId: string) {
  root.innerHTML = `
    <main class="relay-shell relay-shell--session">
      <header class="session-header">
        <a class="back-link" href="/">Live</a>
        <div class="session-header__controls">
          <button id="display-chip" class="${displayModeChipClass('wrap')}">${displayModeChipLabel('wrap')}</button>
          <button id="input-chip" class="${stateChipClass(false)}">${stateChipLabel(false)}</button>
        </div>
      </header>
      <div id="session-status" class="relay-placeholder relay-placeholder--inline" hidden></div>
      <button id="jump-unread" class="session-jump" hidden type="button"></button>
      <section id="terminal" class="relay-terminal"></section>
    </main>
  `

  const terminalElement = document.getElementById('terminal')!
  const status = document.getElementById('session-status') as HTMLDivElement
  const terminal = createTerminal(terminalElement)
  terminal.setDisplayMode('scroll')
  const conn = createLazyWebSocketConnection(relaySessionWebSocketURL(window.location, sessionId))
  const inputChip = document.getElementById('input-chip') as HTMLButtonElement
  const displayChip = document.getElementById('display-chip') as HTMLButtonElement
  const unreadButton = document.getElementById('jump-unread') as HTMLButtonElement
  let inputEnabled = false
  let displayMode: DisplayMode = 'wrap'
  let ptyCols = 0
  let ptyRows = 0

  const terminalAdapter = createSessionPageTerminalAdapter(terminal)
  const controller = createSessionPageController({
    sessionId,
    terminal: terminalAdapter,
    fetchHistory: fetchSessionHistory,
    markRead: async (id, seq) => {
      try {
        await markSessionRead(id, seq)
      } catch (error) {
        showStatus('Failed to update read state. It will retry on refresh.')
        console.error(error)
      }
    },
    attachLive: () => conn.start(),
  })

  inputChip.addEventListener('click', () => {
    inputEnabled = nextInputState(inputEnabled)
    inputChip.textContent = stateChipLabel(inputEnabled)
    inputChip.className = stateChipClass(inputEnabled)
  })

  displayChip.addEventListener('click', () => {
    displayMode = nextDisplayMode(displayMode)
    displayChip.textContent = displayModeChipLabel(displayMode)
    displayChip.className = displayModeChipClass(displayMode)
    if (displayMode === 'scroll' && ptyCols > 0 && ptyRows > 0) {
      terminal.setDisplayMode('scroll', ptyCols, ptyRows)
    } else {
      terminal.setDisplayMode('wrap')
    }
  })

  unreadButton.addEventListener('click', () => {
    void controller.jumpToFirstUnread().then((result) => {
      if (result === 'oldest') {
        showStatus('Older unread history is no longer available.')
      }
      refreshUnreadButton()
    })
  })

  terminal.onData((value) => {
    if (inputEnabled && !isTerminalAutoResponse(value)) {
      conn.send(encodeInput(value))
    }
  })

  conn.onMessage((msg: Message) => {
    if (msg.type === 'output') {
      void controller.appendLiveOutput(msg)
    } else if (msg.type === 'resize') {
      ptyCols = msg.cols
      ptyRows = msg.rows
      if (displayMode === 'scroll') {
        terminal.setDisplayMode('scroll', ptyCols, ptyRows)
      }
    }
  })

  const viewport = terminalElement.querySelector('.xterm-viewport') as HTMLElement | null
  if (viewport) {
    let loadingOlder = false
    viewport.addEventListener(
      'scroll',
      () => {
        if (viewport.scrollTop > 32 || loadingOlder) {
          return
        }
        loadingOlder = true
        void controller.loadOlderHistory().finally(() => {
          loadingOlder = false
        })
      },
      { passive: true },
    )
  }

  void controller.init()
    .then(() => {
      hideStatus()
      refreshUnreadButton()
    })
    .catch((error: unknown) => {
      unreadButton.hidden = true
      const message = error instanceof Error && error.message.includes('404')
        ? 'This session is no longer available.'
        : 'Failed to load session history. Refresh to try again.'
      showStatus(message)
      console.error(error)
    })

  function refreshUnreadButton() {
    const state = controller.getUnreadJumpState()
    unreadButton.hidden = !state.visible
    unreadButton.textContent = state.label
    unreadButton.disabled = !state.visible
  }

  function showStatus(message: string) {
    status.hidden = false
    status.textContent = message
  }

  function hideStatus() {
    status.hidden = true
    status.textContent = ''
  }
}
