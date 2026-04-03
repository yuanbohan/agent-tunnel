import { describe, expect, it } from 'vitest'
import { parseRelayRoute } from './routes'

describe('parseRelayRoute', () => {
  it('returns dashboard for root', () => {
    expect(parseRelayRoute('/')).toEqual({ kind: 'dashboard' })
  })

  it('returns session route for /sessions/:id', () => {
    expect(parseRelayRoute('/sessions/sess-1')).toEqual({ kind: 'session', sessionId: 'sess-1' })
  })
})
