import type { ShardAffinityRule } from '../types/config'
import pm from 'picomatch'

export type ShardAffinityMatcher = (path: string) => number | undefined

export function createShardAffinityMatcher(
  rules: ShardAffinityRule[],
  shardCount: number,
): ShardAffinityMatcher {
  const matchers = rules.map(rule => ({
    isMatch: pm(rule.pattern, { dot: true }),
    shardIndex: Math.min(rule.shardIndex, shardCount - 1),
  }))
  return (path) => {
    return matchers.find(matcher => matcher.isMatch(path))?.shardIndex
  }
}
