export type DurationSmoothing = 'latest' | 'average' | 'p95' | 'median'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export function smoothDurations(observations: DurationObservation[], strategy: DurationSmoothing): number {
  if (!observations.length) {
    return 0
  }
  switch (strategy) {
    case 'average': {
      const sum = observations.reduce((acc, o) => acc + o.duration, 0)
      return Math.round(sum / observations.length)
    }
    case 'p95': {
      const sorted = observations.map(o => o.duration).sort((a, b) => a - b)
      return sorted[Math.max(0, Math.ceil(0.95 * sorted.length) - 1)]
    }
    case 'median': {
      const sorted = observations.map(o => o.duration).sort((a, b) => a - b)
      const mid = Math.floor(sorted.length / 2)
      if (sorted.length % 2 === 0) {
        return Math.floor((sorted[mid - 1] + sorted[mid]) / 2)
      }
      return sorted[mid]
    }
    case 'latest':
    default: {
      let latest = observations[0]
      for (const o of observations) {
        if (o.recordedAt > latest.recordedAt) {
          latest = o
        }
      }
      return latest.duration
    }
  }
}
