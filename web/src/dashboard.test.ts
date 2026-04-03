import { describe, expect, it } from 'vitest'
import { renderSessionCard } from './dashboard'

describe('renderSessionCard', () => {
  it('renders compact launcher-branded cards with unread count', () => {
    const html = renderSessionCard({
      session_id: 'sess-1',
      launcher: 'codex',
      label: 'api-fix',
      cwd: '/tmp/project',
      command_preview: 'codex --profile prod',
      started_at: '2026-04-02T12:00:00Z',
      unread_count: 3,
      preview_b64: btoa('PASS focused test'),
      preview_seq: 17,
      last_active_at: '2026-04-02T12:01:00Z',
    })

    expect(html).toContain('api-fix')
    expect(html).toContain('OpenAI')
    expect(html).toContain('/launchers/openai.ico')
    expect(html).toContain('3 unread')
    expect(html).not.toContain('codex --profile prod')
    expect(html).not.toContain('PASS focused test')
    expect(html).not.toContain('session-card__cwd')
    expect(html).toContain('data-dashboard-preview')
    expect(html).toContain('data-preview-seq="17"')
  })

  it('falls back to cwd basename and exposes the full path only as a tooltip', () => {
    const html = renderSessionCard({
      session_id: 'sess-2',
      launcher: 'gemini',
      cwd: '/Users/demo/worktrees/feature-x',
      command_preview: 'gemini',
      started_at: '2026-04-02T12:00:00Z',
      last_active_at: '2026-04-02T12:01:00Z',
    })

    expect(html).toContain('feature-x')
    expect(html).toContain('Gemini')
    expect(html).toContain('/launchers/gemini.png')
    expect(html).toContain('title="/Users/demo/worktrees/feature-x"')
    expect(html).not.toContain('command_preview')
    expect(html).not.toContain('session-card__command')
  })
})
