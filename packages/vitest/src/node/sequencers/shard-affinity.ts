import pm from 'picomatch'

export interface ShardAffinityRule {
  pattern: string
  shardIndex: number
}

export function matchAffinity(
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
