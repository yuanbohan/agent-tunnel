export interface DashboardPreviewTerminal {
  clear(): void
  setDisplayMode(mode: 'wrap'): void
  write(data: Uint8Array, callback?: () => void): void
  dispose?(): void
}

export async function renderLatestPreviewFrame(
  previewB64: string | undefined,
  terminal: DashboardPreviewTerminal,
): Promise<void> {
  terminal.setDisplayMode('wrap')
  terminal.clear()

  const frame = decodePreviewFrame(previewB64)
  if (!frame || frame.length === 0) {
    return
  }

  await new Promise<void>((resolve) => {
    terminal.write(frame, resolve)
  })
}

export async function mountDashboardPreviews(root: ParentNode): Promise<() => void> {
  const containers = Array.from(root.querySelectorAll<HTMLElement>('[data-dashboard-preview]'))
  const terminals: DashboardPreviewTerminal[] = []
  const { createTerminal } = await import('./terminal')

  for (const container of containers) {
    const terminal = createTerminal(container, {
      cursorBlink: false,
      disableStdin: true,
      fontSize: 12,
    })
    terminals.push(terminal)
    await renderLatestPreviewFrame(container.dataset.previewB64, terminal)
  }

  return () => {
    for (const terminal of terminals) {
      terminal.dispose?.()
    }
  }
}

function decodePreviewFrame(previewB64: string | undefined): Uint8Array | null {
  if (!previewB64) {
    return null
  }

  try {
    const text = atob(previewB64)
    return Uint8Array.from(text, (char) => char.charCodeAt(0))
  } catch {
    return null
  }
}
