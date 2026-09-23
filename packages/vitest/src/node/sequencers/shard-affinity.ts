import type { TestSpecification } from '../test-specification'
import pm from 'picomatch'

export interface ShardAffinityRule {
  pattern: string
  shardIndex: number
}

export interface WeightedSpec {
  spec: TestSpecification
  path: string
  duration: number
}

export function assignByAffinity(
  files: WeightedSpec[],
  shardCount: number,
  rules: ShardAffinityRule[],
  assignTime: (files: WeightedSpec[], shardCount: number, initialLoads?: number[]) => TestSpecification[][],
): TestSpecification[][] {
  const buckets = emptyBuckets(shardCount)
  const loads = Array.from({ length: shardCount }, () => 0)
  const matchers = rules.map(rule => ({
    test: pm(rule.pattern),
    shardIndex: Math.min(rule.shardIndex, shardCount - 1),
  }))
  const unmatched: WeightedSpec[] = []
  let anyMatch = false

  for (const file of files) {
    const match = matchers.find(rule => rule.test(file.path))
    if (!match) {
      unmatched.push(file)
      continue
    }
    anyMatch = true
    buckets[match.shardIndex]!.push(file.spec)
    loads[match.shardIndex] += file.duration
  }

  if (!anyMatch) {
    return assignTime(files, shardCount)
  }

  const rest = assignTime(unmatched, shardCount, loads)
  for (let index = 0; index < shardCount; index++) {
    buckets[index]!.push(...rest[index]!)
  }
  return buckets
}

function emptyBuckets(shardCount: number): TestSpecification[][] {
  return Array.from({ length: shardCount }, () => [])
}
