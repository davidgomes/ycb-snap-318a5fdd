export interface ShardAnalytics {
  files: number
  duration: number
  average: number
}

export function collectShardAnalytics(durations: number[]): ShardAnalytics {
  const duration = durations.reduce((sum, value) => sum + value, 0)
  return { files: durations.length, duration, average: durations.length ? duration / durations.length : 0 }
}
