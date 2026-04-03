import { describe, expect, it } from 'vitest'
import { nextInputState, stateChipLabel } from './relay_session_page'

describe('relay session page state chip', () => {
  it('starts read-only and toggles to input on', () => {
    const next = nextInputState(false)
    expect(next).toBe(true)
    expect(stateChipLabel(false)).toBe('Read-only')
    expect(stateChipLabel(true)).toBe('Input on')
  })
})
