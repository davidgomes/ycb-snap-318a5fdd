import type { DurationObservation } from './duration-history'

export type DurationSmoothingMethod = 'latest' | 'average' | 'p95' | 'median'

export function smoothDuration(
  observations: readonly DurationObservation[],
  method: DurationSmoothingMethod,
): number {
  if (observations.length === 0) {
    return 0
  }

  if (method === 'latest') {
    let best = observations[0]
    for (let index = 1; index < observations.length; index++) {
      const candidate = observations[index]
      if (candidate.recordedAt >= best.recordedAt) {
        best = candidate
      }
    }
    return best.duration
  }

  if (method === 'average') {
    const sum = observations.reduce((total, observation) => total + observation.duration, 0)
    return Math.round(sum / observations.length)
  }

  const durations = observations.map(observation => observation.duration).sort((a, b) => a - b)
  if (method === 'p95') {
    const index = Math.ceil(0.95 * durations.length) - 1
    return durations[index]
  }

  const count = durations.length
  if (count % 2 === 1) {
    return durations[(count - 1) / 2]
  }
  const lower = durations[count / 2 - 1]
  const upper = durations[count / 2]
  return Math.floor((lower + upper) / 2)
}
