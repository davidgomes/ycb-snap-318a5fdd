export function partitionBySlowThreshold<T>(
  files: readonly T[],
  durationOf: (file: T) => number,
  pathOf: (file: T) => string,
  threshold: number,
): { slow: T[]; remaining: T[] } {
  const slow: T[] = []
  const remaining: T[] = []
  for (const file of files) {
    if (durationOf(file) > threshold) {
      slow.push(file)
    }
    else {
      remaining.push(file)
    }
  }

  slow.sort((a, b) => {
    const delta = durationOf(b) - durationOf(a)
    if (delta !== 0) {
      return delta
    }
    return comparePath(pathOf(a), pathOf(b))
  })

  return { slow, remaining }
}

export function assignEqualSplit<T>(
  files: readonly T[],
  shardCount: number,
  pathOf: (file: T) => string,
): T[][] {
  const buckets = emptyBuckets<T>(shardCount)
  const sorted = [...files].sort((a, b) => comparePath(pathOf(a), pathOf(b)))
  sorted.forEach((file, index) => {
    buckets[index % shardCount].push(file)
  })
  return buckets
}

export function assignRoundRobin<T>(
  files: readonly T[],
  shardCount: number,
  durationOf: (file: T) => number,
  pathOf: (file: T) => string,
): T[][] {
  const buckets = emptyBuckets<T>(shardCount)
  if (shardCount <= 0) {
    return buckets
  }

  const sorted = [...files].sort((a, b) => {
    const delta = durationOf(b) - durationOf(a)
    if (delta !== 0) {
      return delta
    }
    return comparePath(pathOf(a), pathOf(b))
  })

  let pointer = 0
  let direction = 1
  for (const file of sorted) {
    buckets[pointer].push(file)
    const next = pointer + direction
    if (next < 0 || next >= shardCount) {
      direction = -direction
      pointer = Math.min(shardCount - 1, Math.max(0, next))
    }
    else {
      pointer = next
    }
  }
  return buckets
}

export function assignLongestProcessingTime<T>(
  files: readonly T[],
  shardCount: number,
  durationOf: (file: T) => number,
  initialLoads?: readonly number[],
): T[][] {
  const buckets = emptyBuckets<T>(shardCount)
  if (shardCount <= 0) {
    return buckets
  }

  const loads = Array.from({ length: shardCount }, (_, index) => initialLoads?.[index] ?? 0)
  const sorted = [...files].sort((a, b) => durationOf(b) - durationOf(a))
  for (const file of sorted) {
    let best = 0
    for (let index = 1; index < shardCount; index++) {
      if (loads[index] < loads[best]) {
        best = index
      }
    }
    buckets[best].push(file)
    loads[best] += durationOf(file)
  }
  return buckets
}

export function shardLoads<T>(
  buckets: readonly (readonly T[])[],
  durationOf: (file: T) => number,
): number[] {
  return buckets.map(bucket =>
    bucket.reduce((total, file) => total + durationOf(file), 0),
  )
}

export function shardRebalanceWarning(loads: readonly number[], threshold: number): string | null {
  if (!(threshold > 0) || loads.length === 0) {
    return null
  }
  const maxLoad = Math.max(...loads)
  const minLoad = Math.min(...loads)
  if (!(maxLoad > 0)) {
    return null
  }
  const ratio = minLoad / maxLoad
  if (ratio < threshold) {
    return `Shard load is imbalanced: ratio=${ratio.toFixed(2)} threshold=${threshold.toFixed(2)}`
  }
  return null
}

function emptyBuckets<T>(shardCount: number): T[][] {
  return Array.from({ length: shardCount }, () => [])
}

function comparePath(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0
}
