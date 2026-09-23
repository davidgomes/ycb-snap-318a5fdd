import type { ShardAffinityRule } from '../types/config'
import type { ShardItem } from './shard-analytics'
import pm from 'picomatch'

export interface AffinityAssignment<T> {
  shards: ShardItem<T>[][]
  unmatched: ShardItem<T>[]
  matchedCount: number
}

export function assignByAffinity<T>(
  items: ShardItem<T>[],
  rules: ShardAffinityRule[],
  shardCount: number,
): AffinityAssignment<T> {
  const matchers = rules.map(rule => ({
    isMatch: pm(rule.pattern, { dot: true }),
    shardIndex: Math.min(Math.max(0, rule.shardIndex), shardCount - 1),
  }))
  const shards: ShardItem<T>[][] = Array.from({ length: shardCount }, () => [])
  const unmatched: ShardItem<T>[] = []
  let matchedCount = 0

  for (const item of items) {
    const rule = matchers.find(matcher => matcher.isMatch(item.path))
    if (rule) {
      shards[rule.shardIndex].push(item)
      matchedCount++
    }
    else {
      unmatched.push(item)
    }
  }

  return { shards, unmatched, matchedCount }
}
