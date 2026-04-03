import { describe, expect, it } from 'vitest'
import { renderLatestPreviewFrame, type DashboardPreviewTerminal } from './dashboard_preview'

function createTerminalStub(events: string[]): DashboardPreviewTerminal {
  return {
    clear() {
      events.push('clear')
    },
    setDisplayMode(mode) {
      events.push(`mode:${mode}`)
    },
    write(data, callback) {
      events.push(`write:${new TextDecoder().decode(data)}`)
      callback?.()
    },
  }
}

describe('renderLatestPreviewFrame', () => {
  it('replaces the previous content and renders only the latest frame in wrap mode', async () => {
    const events: string[] = []
    const terminal = createTerminalStub(events)

    await renderLatestPreviewFrame(btoa('older frame'), terminal)
    await renderLatestPreviewFrame(btoa('latest frame'), terminal)

    expect(events).toEqual([
      'mode:wrap',
      'clear',
      'write:older frame',
      'mode:wrap',
      'clear',
      'write:latest frame',
    ])
  })

  it('keeps the preview empty when there is no latest frame', async () => {
    const events: string[] = []
    const terminal = createTerminalStub(events)

    await renderLatestPreviewFrame(undefined, terminal)

    expect(events).toEqual([
      'mode:wrap',
      'clear',
    ])
  })
})
