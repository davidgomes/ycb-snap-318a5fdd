import type { DurationSmoothing } from '../types/config'
import type { DurationObservation } from './duration-history'

export function smoothDuration(observations: DurationObservation[], method: DurationSmoothing): number {
  if (observations.length === 0) {
    return 0
  }

  switch (method) {
    case 'latest': {
      const latest = observations.reduce((a, b) => (a.recordedAt >= b.recordedAt ? a : b))
      return latest.duration
    }
    case 'average': {
      const sum = observations.reduce((s, o) => s + o.duration, 0)
      return Math.round(sum / observations.length)
    }
    case 'p95': {
      const sorted = [...observations].sort((a, b) => a.duration - b.duration)
      const index = Math.ceil(0.95 * sorted.length) - 1
      return sorted[index].duration
    }
    case 'median': {
      const sorted = [...observations].sort((a, b) => a.duration - b.duration)
      const n = sorted.length
      if (n % 2 === 0) {
        return Math.floor((sorted[n / 2 - 1].duration + sorted[n / 2].duration) / 2)
      }
      return sorted[Math.floor(n / 2)].duration
    }
  }
}
