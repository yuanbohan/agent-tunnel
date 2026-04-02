import { describe, expect, it } from 'vitest'
import { sessionWebSocketURL } from './session_url'

describe('sessionWebSocketURL', () => {
  it('uses ws for http pages', () => {
    expect(sessionWebSocketURL({ protocol: 'http:', host: '127.0.0.1:43127' })).toBe(
      'ws://127.0.0.1:43127/ws',
    )
  })

  it('uses wss for https pages', () => {
    expect(sessionWebSocketURL({ protocol: 'https:', host: 'example.com' })).toBe(
      'wss://example.com/ws',
    )
  })
})
