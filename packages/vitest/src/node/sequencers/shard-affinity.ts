export interface ShardAffinityRule {
  pattern: string
  shardIndex: number
}

function globToRegExp(pattern: string): RegExp {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.')
  return new RegExp(`^${escaped}$`)
}

export function matchShardAffinity(path: string, rules: ShardAffinityRule[]): number | undefined {
  const rule = rules.find(rule => globToRegExp(rule.pattern).test(path))
  return rule?.shardIndex
}
