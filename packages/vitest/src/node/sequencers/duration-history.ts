export interface DurationSample {
  duration: number
  timestamp: number
}

export type DurationHistory = Record<string, DurationSample[]>

export function createDurationHistory(): DurationHistory {
  return Object.create(null) as DurationHistory
}

export function durationEstimate(samples: DurationSample[] | undefined, fallback = 0, smoothing = 0.5): number {
  if (!samples?.length) return fallback
  const alpha = Math.min(1, Math.max(0, smoothing))
  return samples.reduce((estimate, sample) => estimate * (1 - alpha) + sample.duration * alpha, samples[0].duration)
}

export function recordDuration(history: DurationHistory, key: string, duration: number, maxSamples = 20, now = Date.now()): void {
  const samples = history[key] ??= []
  samples.push({ duration: Math.max(0, duration), timestamp: now })
  if (samples.length > maxSamples) samples.splice(0, samples.length - maxSamples)
}
