import type { Logger } from '../logger'
import type { TestSpecification } from '../test-specification'
import type { WeightedSpec } from './shard-affinity'

export function assignByTime(files: WeightedSpec[], shardCount: number, initialLoads: number[] = []): TestSpecification[][] {
  const buckets: TestSpecification[][] = Array.from({ length: shardCount }, () => [])
  const loads = Array.from({ length: shardCount }, (_, index) => initialLoads[index] ?? 0)
  const sorted = [...files].sort((a, b) => b.duration - a.duration || (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
  for (const file of sorted) {
    let best = 0
    for (let index = 1; index < shardCount; index++) {
      if (loads[index]! < loads[best]!) {
        best = index
      }
    }
    buckets[best]!.push(file.spec)
    loads[best] += file.duration
  }
  return buckets
}

export function assignRoundRobin(files: WeightedSpec[], shardCount: number): TestSpecification[][] {
  const buckets: TestSpecification[][] = Array.from({ length: shardCount }, () => [])
  const sorted = [...files].sort((a, b) => b.duration - a.duration || (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
  let pointer = 0
  let direction = 1
  for (const file of sorted) {
    buckets[pointer]!.push(file.spec)
    pointer += direction
    if (pointer < 0 || pointer >= shardCount) {
      pointer = pointer < 0 ? 0 : shardCount - 1
      direction = -direction
    }
  }
  return buckets
}

export function assignEqualSplit(files: WeightedSpec[], shardCount: number): TestSpecification[][] {
  const buckets: TestSpecification[][] = Array.from({ length: shardCount }, () => [])
  const sorted = [...files].sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0)
  sorted.forEach((file, index) => {
    buckets[index % shardCount]!.push(file.spec)
  })
  return buckets
}

export function shardLoads(buckets: TestSpecification[][], durations: Map<TestSpecification, number>): number[] {
  return buckets.map(bucket => bucket.reduce((total, spec) => total + (durations.get(spec) ?? 0), 0))
}

export function warnIfRebalanceNeeded(logger: Logger, loads: number[], threshold: number): void {
  if (!(threshold > 0) || loads.length === 0) {
    return
  }
  const maxLoad = Math.max(...loads)
  if (!(maxLoad > 0)) {
    return
  }
  const minLoad = Math.min(...loads)
  const ratio = minLoad / maxLoad
  if (ratio < threshold) {
    logger.warn(`Shard load imbalance ratio=${ratio.toFixed(2)} threshold=${threshold.toFixed(2)}`)
  }
}

export function isolateSlowFiles(
  files: WeightedSpec[],
  shardCount: number,
  threshold: number,
  distribute: (files: WeightedSpec[], initialLoads: number[]) => TestSpecification[][],
): TestSpecification[][] | null {
  if (!(threshold > 0)) {
    return null
  }
  const slow = files
    .filter(file => file.duration > threshold)
    .sort((a, b) => b.duration - a.duration || (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
  const remaining = files.filter(file => file.duration <= threshold)
  if (slow.length === 0) {
    return null
  }

  const buckets: TestSpecification[][] = Array.from({ length: shardCount }, () => [])
  if (slow.length >= shardCount) {
    for (let index = 0; index < shardCount - 1; index++) {
      buckets[index]!.push(slow[index]!.spec)
    }
    for (const file of slow.slice(shardCount - 1)) {
      buckets[shardCount - 1]!.push(file.spec)
    }
    for (const file of remaining) {
      buckets[shardCount - 1]!.push(file.spec)
    }
    return buckets
  }

  const loads = Array.from({ length: shardCount }, () => 0)
  slow.forEach((file, index) => {
    buckets[index]!.push(file.spec)
    loads[index] = file.duration
  })
  const rest = distribute(remaining, loads)
  for (let index = 0; index < shardCount; index++) {
    buckets[index]!.push(...rest[index]!)
  }
  return buckets
}
