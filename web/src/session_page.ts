export function nextInputState(current: boolean): boolean {
  return !current
}

export function stateChipLabel(enabled: boolean): string {
  return enabled ? 'Input on' : 'Read-only'
}

export function stateChipClass(enabled: boolean): string {
  return enabled ? 'input-chip input-chip--enabled' : 'input-chip'
}
