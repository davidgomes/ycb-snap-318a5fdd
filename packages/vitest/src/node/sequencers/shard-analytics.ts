export function comparePaths(left: string, right: string): number {
  if (left < right) {
    return -1
  }
  if (left > right) {
    return 1
  }
  return 0
}

export function sortByDuration<T>(
  files: readonly T[],
  getDuration: (file: T) => number,
  getPath: (file: T) => string,
): T[] {
  return [...files].sort((left, right) => {
    const durationDiff = getDuration(right) - getDuration(left)
    if (durationDiff !== 0) {
      return durationDiff
    }
    return comparePaths(getPath(left), getPath(right))
  })
}

export function assignLongestProcessingTime<T>(options: {
  files: readonly T[]
  shardCount: number
  getDuration: (file: T) => number
  getPath: (file: T) => string
  buckets?: T[][]
  loads?: number[]
}): T[][] {
  const { files, shardCount, getDuration, getPath } = options
  const buckets = options.buckets ?? Array.from({ length: shardCount }, () => [] as T[])
  const loads = options.loads ?? Array.from({ length: shardCount }, () => 0)
  if (shardCount < 1) {
    return buckets
  }

  const sorted = sortByDuration(files, getDuration, getPath)
  for (const file of sorted) {
    let best = 0
    for (let index = 1; index < shardCount; index++) {
      if (loads[index] < loads[best]) {
        best = index
      }
    }
    buckets[best].push(file)
    loads[best] += getDuration(file)
  }
  return buckets
}

export function assignRoundRobin<T>(
  files: readonly T[],
  shardCount: number,
  getDuration: (file: T) => number,
  getPath: (file: T) => string,
): T[][] {
  const buckets: T[][] = Array.from({ length: Math.max(shardCount, 0) }, () => [])
  if (shardCount < 1) {
    return buckets
  }

  const sorted = sortByDuration(files, getDuration, getPath)
  let pointer = 0
  let direction = 1
  for (const file of sorted) {
    buckets[pointer].push(file)
    const next = pointer + direction
    if (next < 0 || next >= shardCount) {
      direction = -direction
      pointer = next < 0 ? 0 : shardCount - 1
    }
    else {
      pointer = next
    }
  }
  return buckets
}

export function assignEqualSplit<T>(
  files: readonly T[],
  shardCount: number,
  getPath: (file: T) => string,
): T[][] {
  const buckets: T[][] = Array.from({ length: Math.max(shardCount, 0) }, () => [])
  if (shardCount < 1) {
    return buckets
  }

  const sorted = [...files].sort((left, right) => comparePaths(getPath(left), getPath(right)))
  sorted.forEach((file, index) => {
    buckets[index % shardCount].push(file)
  })
  return buckets
}

export function assignIsolatingSlow<T>(options: {
  files: readonly T[]
  shardCount: number
  threshold: number
  getDuration: (file: T) => number
  getPath: (file: T) => string
  assignRemaining: (files: T[], shardCount: number) => T[][]
}): T[][] | null {
  const { files, shardCount, threshold, getDuration, getPath, assignRemaining } = options
  if (!(threshold > 0) || shardCount < 1) {
    return null
  }

  const slow: T[] = []
  const rest: T[] = []
  for (const file of files) {
    if (getDuration(file) > threshold) {
      slow.push(file)
    }
    else {
      rest.push(file)
    }
  }
  if (slow.length === 0) {
    return null
  }

  const sortedSlow = sortByDuration(slow, getDuration, getPath)
  const buckets: T[][] = Array.from({ length: shardCount }, () => [])
  if (sortedSlow.length >= shardCount) {
    for (let index = 0; index < shardCount - 1; index++) {
      buckets[index].push(sortedSlow[index])
    }
    buckets[shardCount - 1].push(...sortedSlow.slice(shardCount - 1), ...rest)
    return buckets
  }

  for (let index = 0; index < sortedSlow.length; index++) {
    buckets[index].push(sortedSlow[index])
  }
  const freeCount = shardCount - sortedSlow.length
  const restBuckets = assignRemaining(rest, freeCount)
  for (let index = 0; index < freeCount; index++) {
    buckets[sortedSlow.length + index].push(...(restBuckets[index] ?? []))
  }
  return buckets
}

export function collectShardLoads<T>(
  buckets: readonly (readonly T[])[],
  getDuration: (file: T) => number,
): number[] {
  return buckets.map(bucket => bucket.reduce((sum, file) => sum + getDuration(file), 0))
}

export function shardLoadRatio(loads: readonly number[]): number {
  if (loads.length === 0) {
    return 1
  }
  let min = loads[0]
  let max = loads[0]
  for (const load of loads) {
    if (load < min) {
      min = load
    }
    if (load > max) {
      max = load
    }
  }
  if (max === 0) {
    return 1
  }
  return min / max
}

export function warnIfShardImbalance(
  warn: (message: string) => void,
  loads: readonly number[],
  threshold: number,
): void {
  if (!(threshold > 0)) {
    return
  }
  const ratio = shardLoadRatio(loads)
  if (ratio < threshold) {
    warn(`Shard load imbalance: ratio=${ratio.toFixed(2)} threshold=${threshold.toFixed(2)}`)
  }
}
