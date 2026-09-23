import type { DurationSmoothing } from '../types/config'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export function smoothDuration(
  observations: readonly DurationObservation[],
  strategy: DurationSmoothing,
): number {
  if (observations.length === 0) {
    return 0
  }

  if (strategy === 'latest') {
    let latest = observations[0]
    for (const observation of observations) {
      if (observation.recordedAt >= latest.recordedAt) {
        latest = observation
      }
    }
    return latest.duration
  }

  if (strategy === 'average') {
    const sum = observations.reduce((total, observation) => total + observation.duration, 0)
    return Math.round(sum / observations.length)
  }

  const sorted = observations.map(observation => observation.duration).sort((left, right) => left - right)
  if (strategy === 'p95') {
    const index = Math.ceil(0.95 * sorted.length) - 1
    return sorted[index]
  }

  if (strategy === 'median') {
    const middle = Math.floor(sorted.length / 2)
    if (sorted.length % 2 === 0) {
      return Math.floor((sorted[middle - 1] + sorted[middle]) / 2)
    }
    return sorted[middle]
  }

  const unknown: never = strategy
  throw new Error(`Unknown duration smoothing strategy: ${String(unknown)}`)
}
