import type { ShardAffinityRule } from '../types/config'
import pm from 'picomatch'
import { assignLongestProcessingTime } from './shard-analytics'

export function assignAffinityShards<T>(options: {
  files: readonly T[]
  shardCount: number
  rules: readonly ShardAffinityRule[]
  pathOf: (file: T) => string
  durationOf: (file: T) => number
  initialLoads?: readonly number[]
}): T[][] | null {
  const {
    files,
    shardCount,
    rules,
    pathOf,
    durationOf,
    initialLoads,
  } = options
  if (shardCount <= 0) {
    return []
  }

  const buckets: T[][] = Array.from({ length: shardCount }, () => [])
  const loads = Array.from({ length: shardCount }, (_, index) => initialLoads?.[index] ?? 0)
  const matchers = rules.map(rule => ({
    match: pm(rule.pattern),
    shardIndex: Math.min(rule.shardIndex, shardCount - 1),
  }))
  const unmatched: T[] = []
  let matched = false

  for (const file of files) {
    const filePath = pathOf(file)
    let shardIndex: number | null = null
    for (const matcher of matchers) {
      if (matcher.match(filePath)) {
        shardIndex = matcher.shardIndex
        break
      }
    }
    if (shardIndex == null) {
      unmatched.push(file)
      continue
    }
    matched = true
    buckets[shardIndex].push(file)
    loads[shardIndex] += durationOf(file)
  }

  if (!matched) {
    return null
  }

  const rest = assignLongestProcessingTime(unmatched, shardCount, durationOf, loads)
  for (let index = 0; index < shardCount; index++) {
    buckets[index].push(...rest[index])
  }
  return buckets
}
