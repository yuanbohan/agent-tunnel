import { describe, expect, it } from 'vitest'
import { renderSessionCard } from './relay_dashboard'

describe('renderSessionCard', () => {
  it('promotes label and launcher before secondary metadata', () => {
    const html = renderSessionCard({
      session_id: 'sess-1',
      launcher: 'codex',
      label: 'api-fix',
      cwd: '/tmp/project',
      command_preview: 'codex --profile prod',
      started_at: '2026-04-02T12:00:00Z',
      last_preview: 'PASS focused test',
      last_active_at: '2026-04-02T12:01:00Z',
    })

    expect(html).toContain('api-fix')
    expect(html).toContain('Codex')
    expect(html).toContain('PASS focused test')
    expect(html.indexOf('api-fix')).toBeLessThan(html.indexOf('/tmp/project'))
  })
})
