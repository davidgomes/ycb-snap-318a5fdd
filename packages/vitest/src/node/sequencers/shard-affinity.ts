import type { ShardAffinityRule } from '../types/config'
import pm from 'picomatch'

export interface ShardAffinityAssignment<T> {
  shards: T[][]
  unmatched: T[]
}

/**
 * Assigns items to shards using the first matching rule. Returns `null` if no rule matched any item.
 */
export function assignShardAffinity<T extends { path: string }>(
  items: T[],
  rules: ShardAffinityRule[],
  shardCount: number,
): ShardAffinityAssignment<T> | null {
  const matchers = rules.map(rule => ({
    isMatch: pm(rule.pattern),
    shardIndex: Math.min(rule.shardIndex, shardCount - 1),
  }))
  const shards: T[][] = Array.from({ length: shardCount }, () => [])
  const unmatched: T[] = []
  let hasMatches = false

  for (const item of items) {
    const rule = matchers.find(({ isMatch }) => isMatch(item.path))
    if (rule) {
      shards[rule.shardIndex].push(item)
      hasMatches = true
    }
    else {
      unmatched.push(item)
    }
  }

  return hasMatches ? { shards, unmatched } : null
}
