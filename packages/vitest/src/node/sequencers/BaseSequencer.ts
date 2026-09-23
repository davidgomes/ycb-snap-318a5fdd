import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { ShardAffinityRule } from '../types/config'
import type { DurationObservation } from './duration-history'
import type { DurationSmoothingMethod } from './duration-smoothing'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import {
  readDurationHistory,
  resolveDurationHistoryFile,
  toDurationHistoryKey,
} from './duration-history'
import { smoothDuration } from './duration-smoothing'
import { assignAffinityShards } from './shard-affinity'
import {
  assignEqualSplit,
  assignLongestProcessingTime,
  assignRoundRobin,
  partitionBySlowThreshold,
  shardLoads,
  shardRebalanceWarning,
} from './shard-analytics'

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
    const history = await this.loadDurationHistory()
    let strategy = sequence?.shardStrategy ?? 'hash'
    if (history == null) {
      strategy = sequence?.durationFallbackStrategy ?? 'hash'
    }

    const pathOf = (spec: TestSpecification) => toDurationHistoryKey(config.root, spec.moduleId)
    const durationOf = (spec: TestSpecification) => this.durationOf(history, pathOf(spec), sequence?.durationSmoothing ?? 'latest')
    const threshold = sequence?.isolateSlowThreshold ?? 0
    const { slow, remaining } = threshold > 0
      ? partitionBySlowThreshold(files, durationOf, pathOf, threshold)
      : { slow: [] as TestSpecification[], remaining: files }

    let buckets: TestSpecification[][]
    if (slow.length === 0) {
      buckets = this.assignByStrategy(files, count, strategy, pathOf, durationOf)
    }
    else {
      buckets = Array.from({ length: count }, () => [])
      const initialLoads = Array.from({ length: count }, () => 0)
      for (let position = 0; position < slow.length; position++) {
        const target = Math.min(position, count - 1)
        buckets[target].push(slow[position])
        initialLoads[target] += durationOf(slow[position])
      }
      if (slow.length >= count) {
        for (const file of remaining) {
          buckets[count - 1].push(file)
        }
      }
      else {
        const rest = this.assignByStrategy(remaining, count, strategy, pathOf, durationOf, initialLoads)
        for (let shard = 0; shard < count; shard++) {
          buckets[shard].push(...rest[shard])
        }
      }
    }

    const warning = shardRebalanceWarning(
      shardLoads(buckets, durationOf),
      sequence?.rebalanceThreshold ?? 0,
    )
    if (warning) {
      this.ctx.logger.warn(warning)
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
    const history = await this.loadDurationHistory()
    const root = this.ctx.config.root
    const smoothing = this.ctx.config.sequence?.durationSmoothing ?? 'latest'
    const ranked = new Map<TestSpecification, { absent: boolean; duration: number }>()
    for (const file of files) {
      const key = toDurationHistoryKey(root, file.moduleId)
      const observations = history?.get(key)
      if (!observations?.length) {
        ranked.set(file, { absent: true, duration: 0 })
      }
      else {
        ranked.set(file, {
          absent: false,
          duration: smoothDuration(observations, smoothing),
        })
      }
    }

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

      const rankA = ranked.get(a)!
      const rankB = ranked.get(b)!
      if (rankA.absent !== rankB.absent) {
        return rankA.absent ? 1 : -1
      }
      return rankB.duration - rankA.duration
    })
  }

  private loadDurationHistory(): Promise<Map<string, DurationObservation[]> | null> {
    const { config } = this.ctx
    const historyPath = config.sequence?.durationHistoryPath ?? 'duration-history.json'
    const ttl = config.sequence?.durationHistoryTTL ?? 0
    return readDurationHistory(resolveDurationHistoryFile(config.root, historyPath), ttl)
  }

  private durationOf(
    history: Map<string, DurationObservation[]> | null,
    key: string,
    smoothing: DurationSmoothingMethod,
  ): number {
    const observations = history?.get(key)
    if (!observations?.length) {
      return 0
    }
    return smoothDuration(observations, smoothing)
  }

  private assignByStrategy(
    files: TestSpecification[],
    count: number,
    strategy: 'hash' | 'time' | 'round-robin' | 'affinity' | 'equal-split',
    pathOf: (spec: TestSpecification) => string,
    durationOf: (spec: TestSpecification) => number,
    initialLoads?: number[],
  ): TestSpecification[][] {
    if (strategy === 'time') {
      return assignLongestProcessingTime(files, count, durationOf, initialLoads)
    }
    if (strategy === 'round-robin') {
      return assignRoundRobin(files, count, durationOf, pathOf)
    }
    if (strategy === 'affinity') {
      const rules = this.ctx.config.sequence?.shardAffinityRules ?? []
      const assigned = assignAffinityShards({
        files,
        shardCount: count,
        rules: rules as readonly ShardAffinityRule[],
        pathOf,
        durationOf,
        initialLoads,
      })
      if (assigned) {
        return assigned
      }
      return assignLongestProcessingTime(files, count, durationOf, initialLoads)
    }
    if (strategy === 'equal-split') {
      return assignEqualSplit(files, count, pathOf)
    }
    return this.assignHash(files, count)
  }

  private assignHash(files: TestSpecification[], count: number): TestSpecification[][] {
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

    const buckets: TestSpecification[][] = Array.from({ length: count }, () => [])
    for (let shardIndex = 1; shardIndex <= count; shardIndex++) {
      const [start, end] = this.calculateShardRange(sorted.length, shardIndex, count)
      buckets[shardIndex - 1] = sorted.slice(start, end).map(({ spec }) => spec)
    }
    return buckets
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
