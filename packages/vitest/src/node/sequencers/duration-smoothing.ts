export interface DurationObservation {
  duration: number
  recordedAt: number
}

export function smoothDurations(
  observations: DurationObservation[],
  strategy: 'latest' | 'average' | 'p95' | 'median',
): number {
  if (!observations.length) return 0
  if (strategy === 'latest') {
    return observations.reduce((a, b) => b.recordedAt >= a.recordedAt ? b : a).duration
  }
  const values = observations.map(o => o.duration).sort((a, b) => a - b)
  if (strategy === 'average') return Math.round(values.reduce((a, b) => a + b, 0) / values.length)
  if (strategy === 'p95') return values[Math.ceil(values.length * 0.95) - 1]
  const middle = Math.floor(values.length / 2)
  return values.length % 2 ? values[middle] : Math.floor((values[middle - 1] + values[middle]) / 2)
}
