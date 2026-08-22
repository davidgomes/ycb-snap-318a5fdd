import type { TestSpecification } from '../test-specification'

export function sortByShardAffinity(files: TestSpecification[], shardCount: number): TestSpecification[] {
  if (shardCount < 2) return [...files]
  return [...files].sort((a, b) => shardIndex(a.moduleId, shardCount) - shardIndex(b.moduleId, shardCount))
}

function shardIndex(path: string, count: number): number {
  let value = 0
  for (const char of path) value = (value * 31 + char.charCodeAt(0)) >>> 0
  return value % count
}
