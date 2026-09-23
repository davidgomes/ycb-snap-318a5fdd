import type { ShardAffinityRule } from '../types/config'
import pm from 'picomatch'

export function matchAffinityShard(
  relativePath: string,
  rules: readonly ShardAffinityRule[],
  shardCount: number,
): number | undefined {
  for (const rule of rules) {
    if (pm.isMatch(relativePath, rule.pattern, { dot: true })) {
      return Math.min(rule.shardIndex, shardCount - 1)
    }
  }
  return undefined
}
