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

  switch (strategy) {
    case 'latest': {
      let best = observations[0]!
      for (let i = 1; i < observations.length; i++) {
        const candidate = observations[i]!
        if (candidate.recordedAt >= best.recordedAt) {
          best = candidate
        }
      }
      return best.duration
    }
    case 'average': {
      let sum = 0
      for (const observation of observations) {
        sum += observation.duration
      }
      return Math.round(sum / observations.length)
    }
    case 'p95': {
      const sorted = observations.map(observation => observation.duration).sort((a, b) => a - b)
      const index = Math.ceil(0.95 * sorted.length) - 1
      return sorted[index]!
    }
    case 'median': {
      const sorted = observations.map(observation => observation.duration).sort((a, b) => a - b)
      const midpoint = Math.floor(sorted.length / 2)
      if (sorted.length % 2 === 1) {
        return sorted[midpoint]!
      }
      return Math.floor((sorted[midpoint - 1]! + sorted[midpoint]!) / 2)
    }
  }
}
