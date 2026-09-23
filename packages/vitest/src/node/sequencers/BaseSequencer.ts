import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { WeightedSpec } from './shard-affinity'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { durationForFile, historyHasFile, normalizeHistoryKey, readDurationHistory } from './duration-history'
import { assignByAffinity } from './shard-affinity'
import { assignByTime, assignEqualSplit, assignRoundRobin, isolateSlowFiles, shardLoads, warnIfRebalanceNeeded } from './shard-analytics'

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
    const history = await readDurationHistory(
      config.root,
      sequence?.durationHistoryPath || 'duration-history.json',
      sequence?.durationHistoryTTL || 0,
    )
    const needsDuration = strategy === 'time' || strategy === 'round-robin' || strategy === 'affinity'
    const resolvedStrategy = history == null && needsDuration
      ? (sequence?.durationFallbackStrategy === 'equal-split' ? 'equal-split' : 'hash')
      : strategy

    const weighted = files.map(spec => this.weightSpec(spec, history, sequence?.durationSmoothing || 'latest'))
    const durations = new Map(weighted.map(file => [file.spec, file.duration]))
    const rules = sequence?.shardAffinityRules || []
    const isolated = history == null
      ? null
      : isolateSlowFiles(
          weighted,
          count,
          sequence?.isolateSlowThreshold || 0,
          (remaining, loads) => this.distribute(remaining, resolvedStrategy, count, loads, rules),
        )

    const buckets = isolated ?? (
      resolvedStrategy === 'hash'
        ? this.hashBuckets(files, count)
        : resolvedStrategy === 'equal-split'
          ? assignEqualSplit(weighted, count)
          : this.distribute(weighted, resolvedStrategy, count, [], rules)
    )

    if (history != null) {
      warnIfRebalanceNeeded(this.ctx.logger, shardLoads(buckets, durations), sequence?.rebalanceThreshold || 0)
    }
    return buckets[index - 1] ?? []
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    const sequence = this.ctx.config.sequence
    if (sequence?.durationBasedSorting) {
      const history = await readDurationHistory(
        this.ctx.config.root,
        sequence.durationHistoryPath || 'duration-history.json',
        sequence.durationHistoryTTL || 0,
      )
      const smoothing = sequence.durationSmoothing || 'latest'
      return [...files].sort((a, b) => {
        const groupOrderDiff = a.project.config.sequence.groupOrder - b.project.config.sequence.groupOrder
        if (groupOrderDiff !== 0) {
          return groupOrderDiff
        }
        const pathA = this.relativePath(a)
        const pathB = this.relativePath(b)
        const absentA = !historyHasFile(history, pathA)
        const absentB = !historyHasFile(history, pathB)
        if (absentA !== absentB) {
          return absentA ? 1 : -1
        }
        return durationForFile(history, pathB, smoothing) - durationForFile(history, pathA, smoothing)
      })
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

  private relativePath(spec: TestSpecification): string {
    const fullPath = resolve(slash(this.ctx.config.root), slash(spec.moduleId))
    return normalizeHistoryKey(relative(this.ctx.config.root, fullPath))
  }

  private weightSpec(spec: TestSpecification, history: Awaited<ReturnType<typeof readDurationHistory>>, smoothing: 'latest' | 'average' | 'p95' | 'median'): WeightedSpec {
    const path = this.relativePath(spec)
    return {
      spec,
      path,
      duration: durationForFile(history, path, smoothing),
    }
  }

  private distribute(
    files: WeightedSpec[],
    strategy: string,
    count: number,
    initialLoads: number[],
    rules: { pattern: string; shardIndex: number }[],
  ): TestSpecification[][] {
    if (strategy === 'hash') {
      return this.hashBuckets(files.map(file => file.spec), count)
    }
    if (strategy === 'equal-split') {
      return assignEqualSplit(files, count)
    }
    if (strategy === 'round-robin') {
      return assignRoundRobin(files, count)
    }
    if (strategy === 'affinity') {
      return assignByAffinity(files, count, rules, (next, shardCount, loads) => assignByTime(next, shardCount, loads))
    }
    return assignByTime(files, count, initialLoads)
  }

  private hashBuckets(files: TestSpecification[], count: number): TestSpecification[][] {
    const sorted = [...files]
      .map((spec) => {
        const fullPath = resolve(slash(this.ctx.config.root), slash(spec.moduleId))
        const specPath = fullPath?.slice(this.ctx.config.root.length)
        return {
          spec,
          hash: hash('sha1', specPath, 'hex'),
        }
      })
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))
    const buckets: TestSpecification[][] = Array.from({ length: count }, () => [])
    for (let index = 1; index <= count; index++) {
      const [shardStart, shardEnd] = this.calculateShardRange(sorted.length, index, count)
      buckets[index - 1] = sorted.slice(shardStart, shardEnd).map(({ spec }) => spec)
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
