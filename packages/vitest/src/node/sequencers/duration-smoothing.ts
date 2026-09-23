export interface DurationObservation {
  duration: number
  recordedAt: number
}

export type DurationSmoothing = 'latest' | 'average' | 'p95' | 'median'

export function smoothDuration(observations: DurationObservation[], smoothing: DurationSmoothing): number {
  if (observations.length === 0) {
    return 0
  }
  if (smoothing === 'latest') {
    let best = observations[0]!
    for (const observation of observations) {
      if (observation.recordedAt >= best.recordedAt) {
        best = observation
      }
    }
    return best.duration
  }
  if (smoothing === 'average') {
    const sum = observations.reduce((total, observation) => total + observation.duration, 0)
    return Math.round(sum / observations.length)
  }
  const sorted = [...observations].sort((a, b) => a.duration - b.duration)
  if (smoothing === 'p95') {
    const index = Math.ceil(0.95 * sorted.length) - 1
    return sorted[index]!.duration
  }
  const mid = Math.floor(sorted.length / 2)
  if (sorted.length % 2 === 0) {
    return Math.floor((sorted[mid - 1]!.duration + sorted[mid]!.duration) / 2)
  }
  return sorted[mid]!.duration
}
