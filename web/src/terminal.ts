import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'

export interface TerminalHandle {
  write(data: Uint8Array): void
  onData(callback: (str: string) => void): void
  onResize(callback: (cols: number, rows: number) => void): void
  currentSize(): { cols: number; rows: number }
  dispose(): void
}

export function createTerminal(container: HTMLElement): TerminalHandle {
  const term = new Terminal({
    theme: {
      background: '#1a1b26',
      foreground: '#a9b1d6',
      cursor: '#c0caf5',
      selectionBackground: '#33467c',
      black: '#15161e',
      red: '#f7768e',
      green: '#9ece6a',
      yellow: '#e0af68',
      blue: '#7aa2f7',
      magenta: '#bb9af7',
      cyan: '#7dcfff',
      white: '#a9b1d6',
    },
    fontFamily: "'JetBrains Mono', 'Fira Code', 'SF Mono', monospace",
    fontSize: 14,
    cursorBlink: true,
  })

  const fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.loadAddon(new WebLinksAddon())
  term.open(container)
  fitAddon.fit()

  let resizeCallback: ((cols: number, rows: number) => void) | null = null

  const observer = new ResizeObserver(() => {
    fitAddon.fit()
    if (resizeCallback) {
      resizeCallback(term.cols, term.rows)
    }
  })
  observer.observe(container)

  return {
    write(data: Uint8Array) {
      term.write(data)
    },
    onData(callback: (str: string) => void) {
      term.onData(callback)
    },
    onResize(callback: (cols: number, rows: number) => void) {
      resizeCallback = callback
    },
    currentSize() {
      return { cols: term.cols, rows: term.rows }
    },
    dispose() {
      observer.disconnect()
      term.dispose()
    },
  }
}
