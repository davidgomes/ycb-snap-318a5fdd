import type { DurationSmoothing } from '../types/config'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export function smoothDuration(
  observations: DurationObservation[],
  strategy: DurationSmoothing,
): number | undefined {
  if (!observations.length) {
    return undefined
  }

  switch (strategy) {
    case 'average': {
      const sum = observations.reduce((total, { duration }) => total + duration, 0)
      return Math.round(sum / observations.length)
    }
    case 'p95': {
      const sorted = sortDurations(observations)
      return sorted[Math.ceil(0.95 * sorted.length) - 1]
    }
    case 'median': {
      const sorted = sortDurations(observations)
      const middle = Math.floor(sorted.length / 2)
      return sorted.length % 2
        ? sorted[middle]
        : Math.floor((sorted[middle - 1] + sorted[middle]) / 2)
    }
    case 'latest':
    default: {
      return observations.reduce((latest, observation) =>
        observation.recordedAt >= latest.recordedAt ? observation : latest,
      ).duration
    }
  }
}

function sortDurations(observations: DurationObservation[]): number[] {
  return observations.map(({ duration }) => duration).sort((a, b) => a - b)
}
