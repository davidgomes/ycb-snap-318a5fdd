import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { ShardStrategyName } from '../types/config'
import type { DurationObservation } from './duration-smoothing'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { readDurationHistory, toDurationHistoryKey } from './duration-history'
import { smoothDuration } from './duration-smoothing'
import { matchAffinityShard } from './shard-affinity'
import { rebalanceWarning } from './shard-analytics'

export class BaseSequencer implements TestSequencer {
  protected ctx: Vitest

  constructor(ctx: Vitest) {
    this.ctx = ctx
  }

  // async so it can be extended by other sequelizers
  public async shard(files: TestSpecification[]): Promise<TestSpecification[]> {
    const { index, count } = this.ctx.config.shard!
    const { buckets, durations } = await this.assignShards(files, count)
    this.warnIfRebalanced(buckets, durations)
    return buckets[index - 1] ?? []
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    const cache = this.ctx.cache
    const durationSorting = this.ctx.config.sequence?.durationBasedSorting === true
    const history = durationSorting ? await this.loadHistory() : null
    const smoothing = this.ctx.config.sequence?.durationSmoothing ?? 'latest'

    return [...files].sort((a, b) => {
      // "sequence.groupOrder" is higher priority
      const groupOrderDiff = a.project.config.sequence.groupOrder - b.project.config.sequence.groupOrder
      if (groupOrderDiff !== 0) {
        return groupOrderDiff
      }

      // Projects run sequential
      if (a.project.name !== b.project.name) {
        return a.project.name < b.project.name ? -1 : 1
      }

      // Isolated run first
      if (a.project.config.isolate && !b.project.config.isolate) {
        return -1
      }
      if (!a.project.config.isolate && b.project.config.isolate) {
        return 1
      }

      if (durationSorting) {
        return this.compareDuration(a, b, history, smoothing)
      }

      const keyA = `${a.project.name}:${relative(this.ctx.config.root, a.moduleId)}`
      const keyB = `${b.project.name}:${relative(this.ctx.config.root, b.moduleId)}`

      const aState = cache.getFileTestResults(keyA)
      const bState = cache.getFileTestResults(keyB)

      if (!aState || !bState) {
        const statsA = cache.getFileStats(keyA)
        const statsB = cache.getFileStats(keyB)

        // run unknown first
        if (!statsA || !statsB) {
          return !statsA && statsB ? -1 : !statsB && statsA ? 1 : 0
        }

        // run larger files first
        return statsB.size - statsA.size
      }

      // run failed first
      if (aState.failed && !bState.failed) {
        return -1
      }
      if (!aState.failed && bState.failed) {
        return 1
      }

      // run longer first
      return bState.duration - aState.duration
    })
  }

  private async assignShards(
    files: TestSpecification[],
    count: number,
  ): Promise<{ buckets: TestSpecification[][]; durations: Map<TestSpecification, number> }> {
    const history = await this.loadHistory()
    if (history == null) {
      return {
        buckets: this.splitWithoutHistory(files, count),
        durations: new Map(files.map(file => [file, 0])),
      }
    }

    const durations = this.durationMap(files, history)
    const threshold = this.ctx.config.sequence?.isolateSlowThreshold ?? 0
    if (threshold > 0) {
      const slow = files.filter(file => (durations.get(file) ?? 0) > threshold)
      if (slow.length > 0) {
        return {
          buckets: this.isolateSlow(files, durations, count, slow),
          durations,
        }
      }
    }

    return {
      buckets: this.splitByStrategy(files, durations, count, this.shardStrategy()),
      durations,
    }
  }

  private splitWithoutHistory(files: TestSpecification[], count: number): TestSpecification[][] {
    const fallback = this.ctx.config.sequence?.durationFallbackStrategy ?? 'hash'
    if (fallback === 'equal-split') {
      return this.equalSplit(files, count)
    }
    return this.hashSplit(files, count)
  }

  private splitByStrategy(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
    count: number,
    strategy: ShardStrategyName,
  ): TestSpecification[][] {
    switch (strategy) {
      case 'time':
        return this.timeSplit(files, durations, count)
      case 'round-robin':
        return this.roundRobinSplit(files, durations, count)
      case 'affinity':
        return this.affinitySplit(files, durations, count)
      case 'hash':
        return this.hashSplit(files, count)
    }
  }

  private isolateSlow(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
    count: number,
    slow: TestSpecification[],
  ): TestSpecification[][] {
    const sortedSlow = this.sortByDuration(slow, durations)
    const slowSet = new Set(slow)
    const rest = files.filter(file => !slowSet.has(file))
    const buckets = Array.from({ length: count }, () => [] as TestSpecification[])
    const pinned = Math.min(sortedSlow.length, count)

    for (let i = 0; i < pinned; i++) {
      buckets[i]!.push(sortedSlow[i]!)
    }

    if (sortedSlow.length >= count) {
      buckets[count - 1]!.push(...sortedSlow.slice(count), ...rest)
      return buckets
    }

    const freeCount = count - sortedSlow.length
    const freeBuckets = this.splitByStrategy(rest, durations, freeCount, this.shardStrategy())
    for (let i = 0; i < freeCount; i++) {
      buckets[sortedSlow.length + i]!.push(...freeBuckets[i]!)
    }
    return buckets
  }

  private hashSplit(files: TestSpecification[], count: number): TestSpecification[][] {
    const { config } = this.ctx
    const sorted = [...files]
      .map((spec) => {
        const fullPath = resolve(slash(config.root), slash(spec.moduleId))
        const specPath = fullPath?.slice(config.root.length)
        return {
          spec,
          hash: hash('sha1', specPath, 'hex'),
        }
      })
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))

    const buckets = Array.from({ length: count }, () => [] as TestSpecification[])
    for (let index = 1; index <= count; index++) {
      const [shardStart, shardEnd] = this.calculateShardRange(sorted.length, index, count)
      buckets[index - 1] = sorted.slice(shardStart, shardEnd).map(({ spec }) => spec)
    }
    return buckets
  }

  private equalSplit(files: TestSpecification[], count: number): TestSpecification[][] {
    const sorted = [...files].sort((a, b) => comparePath(this.relativePath(a), this.relativePath(b)))
    const buckets = Array.from({ length: count }, () => [] as TestSpecification[])
    sorted.forEach((file, index) => {
      buckets[index % count]!.push(file)
    })
    return buckets
  }

  private timeSplit(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
    count: number,
  ): TestSpecification[][] {
    const buckets = Array.from({ length: count }, () => [] as TestSpecification[])
    const loads = Array.from({ length: count }, () => 0)
    this.assignLongestProcessingTime(files, durations, buckets, loads)
    return buckets
  }

  private roundRobinSplit(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
    count: number,
  ): TestSpecification[][] {
    const sorted = this.sortByDuration(files, durations)
    const buckets = Array.from({ length: count }, () => [] as TestSpecification[])
    let pointer = 0
    let direction = 1
    for (const file of sorted) {
      buckets[pointer]!.push(file)
      const next = pointer + direction
      if (next < 0 || next >= count) {
        direction *= -1
        pointer = next < 0 ? 0 : count - 1
      }
      else {
        pointer = next
      }
    }
    return buckets
  }

  private affinitySplit(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
    count: number,
  ): TestSpecification[][] {
    const rules = this.ctx.config.sequence?.shardAffinityRules ?? []
    const buckets = Array.from({ length: count }, () => [] as TestSpecification[])
    const loads = Array.from({ length: count }, () => 0)
    const unmatched: TestSpecification[] = []
    let matched = false

    for (const file of files) {
      const shardIndex = matchAffinityShard(this.relativePath(file), rules, count)
      if (shardIndex == null) {
        unmatched.push(file)
        continue
      }
      matched = true
      buckets[shardIndex]!.push(file)
      loads[shardIndex]! += durations.get(file) ?? 0
    }

    if (!matched) {
      return this.timeSplit(files, durations, count)
    }

    this.assignLongestProcessingTime(unmatched, durations, buckets, loads)
    return buckets
  }

  private assignLongestProcessingTime(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
    buckets: TestSpecification[][],
    loads: number[],
  ): void {
    const sorted = this.sortByDuration(files, durations)
    for (const file of sorted) {
      let best = 0
      for (let index = 1; index < loads.length; index++) {
        if (loads[index]! < loads[best]!) {
          best = index
        }
      }
      buckets[best]!.push(file)
      loads[best]! += durations.get(file) ?? 0
    }
  }

  private sortByDuration(
    files: TestSpecification[],
    durations: Map<TestSpecification, number>,
  ): TestSpecification[] {
    return [...files].sort((a, b) => {
      const diff = (durations.get(b) ?? 0) - (durations.get(a) ?? 0)
      if (diff !== 0) {
        return diff
      }
      return comparePath(this.relativePath(a), this.relativePath(b))
    })
  }

  private durationMap(
    files: TestSpecification[],
    history: Map<string, DurationObservation[]>,
  ): Map<TestSpecification, number> {
    const smoothing = this.ctx.config.sequence?.durationSmoothing ?? 'latest'
    const durations = new Map<TestSpecification, number>()
    for (const file of files) {
      const observations = history.get(this.relativePath(file))
      durations.set(
        file,
        observations && observations.length > 0 ? smoothDuration(observations, smoothing) : 0,
      )
    }
    return durations
  }

  private compareDuration(
    a: TestSpecification,
    b: TestSpecification,
    history: Map<string, DurationObservation[]> | null,
    smoothing: NonNullable<Vitest['config']['sequence']>['durationSmoothing'],
  ): number {
    const aObservations = history?.get(this.relativePath(a))
    const bObservations = history?.get(this.relativePath(b))
    const aKnown = !!aObservations?.length
    const bKnown = !!bObservations?.length
    if (aKnown && !bKnown) {
      return -1
    }
    if (!aKnown && bKnown) {
      return 1
    }
    if (!aKnown || !bKnown || !aObservations || !bObservations) {
      return 0
    }
    return smoothDuration(bObservations, smoothing) - smoothDuration(aObservations, smoothing)
  }

  private warnIfRebalanced(
    buckets: TestSpecification[][],
    durations: Map<TestSpecification, number>,
  ): void {
    const threshold = this.ctx.config.sequence?.rebalanceThreshold ?? 0
    const loads = buckets.map(bucket => bucket.reduce((sum, file) => sum + (durations.get(file) ?? 0), 0))
    const message = rebalanceWarning(loads, threshold)
    if (message) {
      this.ctx.logger?.warn(message)
    }
  }

  private shardStrategy(): ShardStrategyName {
    return this.ctx.config.sequence?.shardStrategy ?? 'hash'
  }

  private async loadHistory(): Promise<Map<string, DurationObservation[]> | null> {
    const root = this.ctx.config.root
    if (!root) {
      return null
    }
    const sequence = this.ctx.config.sequence
    return readDurationHistory(root, {
      historyPath: sequence?.durationHistoryPath ?? 'duration-history.json',
      ttl: sequence?.durationHistoryTTL ?? 0,
    })
  }

  private relativePath(spec: TestSpecification): string {
    return toDurationHistoryKey(this.ctx.config.root, spec.moduleId)
  }

  // Calculate distributed shard range [start, end] distributed equally
  private calculateShardRange(filesCount: number, index: number, count: number): [number, number] {
    const baseShardSize = Math.floor(filesCount / count)
    const remainderTestFilesCount = filesCount % count
    if (remainderTestFilesCount >= index) {
      const shardSize = baseShardSize + 1
      const shardStart = shardSize * (index - 1)
      const shardEnd = shardSize * index
      return [shardStart, shardEnd]
    }

    const shardStart = remainderTestFilesCount * (baseShardSize + 1) + (index - remainderTestFilesCount - 1) * baseShardSize
    const shardEnd = shardStart + baseShardSize
    return [shardStart, shardEnd]
  }
}

function comparePath(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}
