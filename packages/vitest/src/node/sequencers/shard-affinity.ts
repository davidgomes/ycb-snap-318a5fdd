import type { ShardAffinityRule } from '../types/config'
import pm from 'picomatch'

export function matchAffinityShard(
  path: string,
  rules: ShardAffinityRule[],
  shardCount: number,
): number | undefined {
  for (const rule of rules) {
    if (pm.isMatch(path, rule.pattern)) {
      return Math.min(rule.shardIndex, shardCount - 1)
    }
  }
  return undefined
}

export function anyRuleMatches(paths: string[], rules: ShardAffinityRule[]): boolean {
  return paths.some(path => rules.some(rule => pm.isMatch(path, rule.pattern)))
}
