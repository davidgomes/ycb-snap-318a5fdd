import type { ShardAffinityRule } from '../types/config'
import pm from 'picomatch'
import { assignLongestProcessingTime, comparePaths } from './shard-analytics'

export function assignAffinityShards<T>(options: {
  files: readonly T[]
  shardCount: number
  rules: readonly ShardAffinityRule[]
  getPath: (file: T) => string
  getDuration: (file: T) => number
}): T[][] | null {
  const { files, shardCount, rules, getPath, getDuration } = options
  if (shardCount < 1) {
    return null
  }

  const matchers = rules.map(rule => ({
    match: pm(rule.pattern),
    shardIndex: rule.shardIndex,
  }))
  const buckets: T[][] = Array.from({ length: shardCount }, () => [])
  const loads = Array.from({ length: shardCount }, () => 0)
  const unmatched: T[] = []
  let matched = 0
  const ordered = [...files].sort((left, right) => comparePaths(getPath(left), getPath(right)))

  for (const file of ordered) {
    const filePath = getPath(file)
    let shardIndex: number | undefined
    for (const matcher of matchers) {
      if (matcher.match(filePath)) {
        shardIndex = Math.min(Math.max(matcher.shardIndex, 0), shardCount - 1)
        break
      }
    }
    if (shardIndex == null) {
      unmatched.push(file)
      continue
    }
    matched++
    buckets[shardIndex].push(file)
    loads[shardIndex] += getDuration(file)
  }

  if (matched === 0) {
    return null
  }

  return assignLongestProcessingTime({
    files: unmatched,
    shardCount,
    getDuration,
    getPath,
    buckets,
    loads,
  })
}
