import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { DurationFallbackStrategy, ShardStrategy } from '../types/config'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { readProjectDurationHistory, relativeModulePath, resolveFileDurations } from './duration-history'
import { assignAffinityShards } from './shard-affinity'
import { assignEqualSplit, assignIsolatingSlow, assignLongestProcessingTime, assignRoundRobin, collectShardLoads, warnIfShardImbalance } from './shard-analytics'

export class BaseSequencer implements TestSequencer {
  protected ctx: Vitest

  constructor(ctx: Vitest) {
    this.ctx = ctx
  }

  // async so it can be extended by other sequelizers
  public async shard(files: TestSpecification[]): Promise<TestSpecification[]> {
    const { config } = this.ctx
    const { index, count } = config.shard!
    const sequence = config.sequence
    const strategy = sequence?.shardStrategy ?? 'hash'
    const isolateSlowThreshold = sequence?.isolateSlowThreshold ?? 0
    const rebalanceThreshold = sequence?.rebalanceThreshold ?? 0
    const fallback = sequence?.durationFallbackStrategy ?? 'hash'
    const needsHistory = strategy !== 'hash' || isolateSlowThreshold > 0 || rebalanceThreshold > 0

    if (!needsHistory) {
      return this.hashSlice(files, index, count)
    }

    const history = await readProjectDurationHistory(config.root, sequence)
    let effectiveStrategy: ShardStrategy | DurationFallbackStrategy = strategy
    if (strategy !== 'hash' && history == null) {
      effectiveStrategy = fallback
    }

    const durations = resolveFileDurations(
      files,
      history,
      sequence?.durationSmoothing ?? 'latest',
      spec => this.filePath(spec),
    )
    const getDuration = (spec: TestSpecification) => durations.get(spec)?.duration ?? 0
    const getPath = (spec: TestSpecification) => this.filePath(spec)
    const assign = (input: TestSpecification[], shardCount: number) => {
      return this.assignByStrategy(input, shardCount, effectiveStrategy, getDuration, getPath)
    }

    let buckets: TestSpecification[][] | null = null
    if (isolateSlowThreshold > 0 && history != null) {
      buckets = assignIsolatingSlow({
        files,
        shardCount: count,
        threshold: isolateSlowThreshold,
        getDuration,
        getPath,
        assignRemaining: assign,
      })
    }

    if (!buckets) {
      if (effectiveStrategy === 'hash' && !(rebalanceThreshold > 0 && history != null)) {
        return this.hashSlice(files, index, count)
      }
      buckets = assign(files, count)
    }

    if (rebalanceThreshold > 0 && history != null) {
      warnIfShardImbalance(
        message => this.ctx.logger.warn(message),
        collectShardLoads(buckets, getDuration),
        rebalanceThreshold,
      )
    }

    return buckets[index - 1] ?? []
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    if (this.ctx.config.sequence?.durationBasedSorting) {
      return this.sortByRecordedDuration(files)
    }

    const cache = this.ctx.cache
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

  private async sortByRecordedDuration(files: TestSpecification[]): Promise<TestSpecification[]> {
    const sequence = this.ctx.config.sequence
    const history = await readProjectDurationHistory(this.ctx.config.root, sequence)
    const durations = resolveFileDurations(
      files,
      history,
      sequence?.durationSmoothing ?? 'latest',
      spec => this.filePath(spec),
    )

    return [...files].sort((a, b) => {
      const groupOrderDiff = a.project.config.sequence.groupOrder - b.project.config.sequence.groupOrder
      if (groupOrderDiff !== 0) {
        return groupOrderDiff
      }

      if (a.project.name !== b.project.name) {
        return a.project.name < b.project.name ? -1 : 1
      }

      if (a.project.config.isolate && !b.project.config.isolate) {
        return -1
      }
      if (!a.project.config.isolate && b.project.config.isolate) {
        return 1
      }

      const aInfo = durations.get(a)
      const bInfo = durations.get(b)
      if (Boolean(aInfo?.known) !== Boolean(bInfo?.known)) {
        return aInfo?.known ? -1 : 1
      }
      if (aInfo?.known && bInfo?.known && aInfo.duration !== bInfo.duration) {
        return bInfo.duration - aInfo.duration
      }
      return 0
    })
  }

  private filePath(spec: TestSpecification): string {
    return relativeModulePath(this.ctx.config.root, spec.moduleId)
  }

  private assignByStrategy(
    files: TestSpecification[],
    shardCount: number,
    strategy: ShardStrategy | DurationFallbackStrategy,
    getDuration: (spec: TestSpecification) => number,
    getPath: (spec: TestSpecification) => string,
  ): TestSpecification[][] {
    if (strategy === 'hash') {
      return this.hashBuckets(files, shardCount)
    }
    if (strategy === 'equal-split') {
      return assignEqualSplit(files, shardCount, getPath)
    }
    if (strategy === 'round-robin') {
      return assignRoundRobin(files, shardCount, getDuration, getPath)
    }
    if (strategy === 'affinity') {
      const assigned = assignAffinityShards({
        files,
        shardCount,
        rules: this.ctx.config.sequence?.shardAffinityRules ?? [],
        getPath,
        getDuration,
      })
      if (assigned) {
        return assigned
      }
    }
    return assignLongestProcessingTime({
      files,
      shardCount,
      getDuration,
      getPath,
    })
  }

  private hashSlice(files: TestSpecification[], index: number, count: number): TestSpecification[] {
    const sorted = this.hashSorted(files)
    const [shardStart, shardEnd] = this.calculateShardRange(files.length, index, count)
    return sorted.slice(shardStart, shardEnd)
  }

  private hashBuckets(files: TestSpecification[], count: number): TestSpecification[][] {
    const sorted = this.hashSorted(files)
    return Array.from({ length: count }, (_, offset) => {
      const [shardStart, shardEnd] = this.calculateShardRange(files.length, offset + 1, count)
      return sorted.slice(shardStart, shardEnd)
    })
  }

  private hashSorted(files: TestSpecification[]): TestSpecification[] {
    const { config } = this.ctx
    return [...files]
      .map((spec) => {
        const fullPath = resolve(slash(config.root), slash(spec.moduleId))
        const specPath = fullPath?.slice(config.root.length)
        return {
          spec,
          hash: hash('sha1', specPath, 'hex'),
        }
      })
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))
      .map(({ spec }) => spec)
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
