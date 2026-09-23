import type { DurationSmoothing } from '../types/config'
import type { DurationObservation } from './duration-history'

export function smoothDuration(
  observations: DurationObservation[],
  strategy: DurationSmoothing,
): number | undefined {
  if (!observations.length) {
    return undefined
  }
  switch (strategy) {
    case 'average': {
      const sum = observations.reduce((acc, o) => acc + o.duration, 0)
      return Math.round(sum / observations.length)
    }
    case 'p95': {
      const sorted = sortedDurations(observations)
      return sorted[Math.ceil(0.95 * sorted.length) - 1]
    }
    case 'median': {
      const sorted = sortedDurations(observations)
      const middle = Math.floor(sorted.length / 2)
      if (sorted.length % 2 === 0) {
        return Math.floor((sorted[middle - 1] + sorted[middle]) / 2)
      }
      return sorted[middle]
    }
    case 'latest':
    default: {
      let latest = observations[0]
      for (const observation of observations) {
        if (observation.recordedAt > latest.recordedAt) {
          latest = observation
        }
      }
      return latest.duration
    }
  }
}

function sortedDurations(observations: DurationObservation[]): number[] {
  return observations.map(o => o.duration).sort((a, b) => a - b)
}
