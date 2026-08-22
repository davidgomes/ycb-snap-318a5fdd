export function smoothDuration(previous: number | undefined, current: number, factor = 0.5): number {
  if (previous == null) return current
  const weight = Math.min(1, Math.max(0, factor))
  return previous * (1 - weight) + current * weight
}
