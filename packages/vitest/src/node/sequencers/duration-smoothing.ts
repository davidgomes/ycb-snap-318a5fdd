import type { SequenceDurationSmoothing } from '../types/config'
import type { DurationObservation } from './duration-history'

export function smoothDuration(
  observations: DurationObservation[],
  strategy: SequenceDurationSmoothing,
): number | undefined {
  if (!observations.length) {
    return undefined
  }

  switch (strategy) {
    case 'latest': {
      let latest = observations[0]
      for (const observation of observations) {
        if (observation.recordedAt > latest.recordedAt) {
          latest = observation
        }
      }
      return latest.duration
    }
    case 'average': {
      const sum = observations.reduce((total, observation) => total + observation.duration, 0)
      return Math.round(sum / observations.length)
    }
    case 'p95': {
      const sorted = sortDurations(observations)
      return sorted[Math.ceil(0.95 * sorted.length) - 1]
    }
    case 'median': {
      const sorted = sortDurations(observations)
      const middle = Math.floor(sorted.length / 2)
      if (sorted.length % 2 === 0) {
        return Math.floor((sorted[middle - 1] + sorted[middle]) / 2)
      }
      return sorted[middle]
    }
  }
}

function sortDurations(observations: DurationObservation[]): number[] {
  return observations.map(observation => observation.duration).sort((a, b) => a - b)
}
