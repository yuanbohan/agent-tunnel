import { Terminal, type IDecoration, type IDecorationOptions } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'

export type DisplayMode = 'scroll' | 'wrap'
export type TerminalMarker = {
  line: number | undefined
}

export type TerminalDecoration = Pick<IDecoration, 'onRender' | 'dispose'>
export type TerminalOptions = {
  cursorBlink?: boolean
  disableStdin?: boolean
  fontSize?: number
}
export type TerminalDecorationOptions = Omit<IDecorationOptions, 'marker'> & {
  marker: TerminalMarker
}

export interface TerminalHandle {
  write(data: Uint8Array, callback?: () => void): void
  onData(callback: (str: string) => void): void
  onResize(callback: (cols: number, rows: number) => void): void
  currentSize(): { cols: number; rows: number }
  setDisplayMode(mode: DisplayMode, ptyCols?: number, ptyRows?: number): void
  clear(): void
  registerMarker(cursorYOffset?: number): TerminalMarker | undefined
  registerDecoration(options: TerminalDecorationOptions): TerminalDecoration | undefined
  scrollToLine(line: number): void
  dispose(): void
}

export function createTerminal(container: HTMLElement, options: TerminalOptions = {}): TerminalHandle {
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
    fontSize: options.fontSize ?? 14,
    cursorBlink: options.cursorBlink ?? true,
    disableStdin: options.disableStdin ?? false,
  })

  const fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.loadAddon(new WebLinksAddon())
  term.open(container)

  let currentMode: DisplayMode = 'scroll'
  let resizeCallback: ((cols: number, rows: number) => void) | null = null

  const observer = new ResizeObserver(() => {
    if (currentMode === 'wrap') {
      fitAddon.fit()
    }
    if (resizeCallback) {
      resizeCallback(term.cols, term.rows)
    }
  })
  observer.observe(container)

  return {
    write(data: Uint8Array, callback?: () => void) {
      term.write(data, callback)
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
    setDisplayMode(mode: DisplayMode, ptyCols?: number, ptyRows?: number) {
      currentMode = mode
      if (mode === 'scroll' && ptyCols && ptyRows) {
        term.resize(ptyCols, ptyRows)
      } else if (mode === 'wrap') {
        fitAddon.fit()
      }
    },
    clear() {
      term.clear()
    },
    registerMarker(cursorYOffset?: number) {
      return term.registerMarker(cursorYOffset)
    },
    registerDecoration(options: TerminalDecorationOptions) {
      return term.registerDecoration(options as IDecorationOptions)
    },
    scrollToLine(line: number) {
      term.scrollToLine(line)
    },
    dispose() {
      observer.disconnect()
      term.dispose()
    },
  }
}
