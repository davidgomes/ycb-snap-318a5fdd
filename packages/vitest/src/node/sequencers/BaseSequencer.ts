import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { ResolvedConfig } from '../types/config'
import type { ShardItem } from './shard-analytics'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { getDurationHistoryKey, loadFileDurations } from './duration-history'
import { assignByAffinity } from './shard-affinity'
import { warnIfUnbalanced } from './shard-analytics'

type SequenceConfig = Partial<ResolvedConfig['sequence']>
type SpecItem = ShardItem<TestSpecification>

export class BaseSequencer implements TestSequencer {
  protected ctx: Vitest

  constructor(ctx: Vitest) {
    this.ctx = ctx
  }

  // async so it can be extended by other sequelizers
  public async shard(files: TestSpecification[]): Promise<TestSpecification[]> {
    const { config } = this.ctx
    const { index, count } = config.shard!
    const sequence: SequenceConfig = config.sequence ?? {}
    const strategy = sequence.shardStrategy ?? 'hash'
    const isolateSlowThreshold = sequence.isolateSlowThreshold ?? 0
    const rebalanceThreshold = sequence.rebalanceThreshold ?? 0

    if (strategy === 'hash' && isolateSlowThreshold <= 0 && rebalanceThreshold <= 0) {
      const [shardStart, shardEnd] = this.calculateShardRange(files.length, index, count)
      return this.sortByHash(files).slice(shardStart, shardEnd)
    }

    const durations = loadFileDurations(config.root, sequence)
    const items: SpecItem[] = files.map((spec) => {
      const path = getDurationHistoryKey(config.root, spec.moduleId)
      return { item: spec, path, duration: durations?.get(path) ?? 0 }
    })

    const shards = this.partition(items, count, sequence, durations !== null)
    warnIfUnbalanced(shards, rebalanceThreshold, this.ctx.logger)
    return (shards[index - 1] ?? []).map(({ item }) => item)
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    const cache = this.ctx.cache
    const sequence: SequenceConfig = this.ctx.config.sequence ?? {}
    const durations = sequence.durationBasedSorting
      ? loadFileDurations(this.ctx.config.root, sequence)
      : null
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

      if (durations) {
        const durationA = durations.get(getDurationHistoryKey(this.ctx.config.root, a.moduleId))
        const durationB = durations.get(getDurationHistoryKey(this.ctx.config.root, b.moduleId))
        if (durationA !== undefined || durationB !== undefined) {
          if (durationA === undefined) {
            return 1
          }
          if (durationB === undefined) {
            return -1
          }
          return durationB - durationA
        }
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

  private partition(
    items: SpecItem[],
    count: number,
    sequence: SequenceConfig,
    hasHistory: boolean,
  ): SpecItem[][] {
    const threshold = sequence.isolateSlowThreshold ?? 0
    const slow = threshold > 0
      ? sortByDuration(items.filter(item => item.duration > threshold))
      : []

    if (!slow.length) {
      return this.distribute(items, count, sequence, hasHistory)
    }

    const remaining = items.filter(item => !slow.includes(item))
    if (slow.length >= count) {
      const shards = slow.slice(0, count - 1).map(item => [item])
      shards.push([...slow.slice(count - 1), ...remaining])
      return shards
    }

    return [
      ...slow.map(item => [item]),
      ...this.distribute(remaining, count - slow.length, sequence, hasHistory),
    ]
  }

  private distribute(
    items: SpecItem[],
    count: number,
    sequence: SequenceConfig,
    hasHistory: boolean,
  ): SpecItem[][] {
    const strategy = sequence.shardStrategy ?? 'hash'
    if (strategy === 'hash') {
      return this.distributeByHash(items, count)
    }
    if (!hasHistory) {
      if (sequence.durationFallbackStrategy === 'equal-split') {
        return distributeEqually(items, count)
      }
      return this.distributeByHash(items, count)
    }
    if (strategy === 'round-robin') {
      return distributeRoundRobin(items, count)
    }
    if (strategy === 'affinity') {
      const { shards, unmatched, matchedCount } = assignByAffinity(
        items,
        sequence.shardAffinityRules ?? [],
        count,
      )
      if (matchedCount === 0) {
        return distributeByTime(items, count)
      }
      return distributeByTime(unmatched, count, shards)
    }
    return distributeByTime(items, count)
  }

  private distributeByHash(items: SpecItem[], count: number): SpecItem[][] {
    const byModule = new Map(items.map(item => [item.item, item]))
    const sorted = this.sortByHash(items.map(({ item }) => item))
    return Array.from({ length: count }, (_, i) => {
      const [start, end] = this.calculateShardRange(sorted.length, i + 1, count)
      return sorted.slice(start, end).map(spec => byModule.get(spec)!)
    })
  }

  private sortByHash(files: TestSpecification[]): TestSpecification[] {
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

function comparePaths(a: SpecItem, b: SpecItem): number {
  return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
}

function sortByDuration(items: SpecItem[]): SpecItem[] {
  return [...items].sort((a, b) => b.duration - a.duration || comparePaths(a, b))
}

function distributeEqually(items: SpecItem[], count: number): SpecItem[][] {
  const shards: SpecItem[][] = Array.from({ length: count }, () => [])
  ;[...items].sort(comparePaths).forEach((item, i) => shards[i % count].push(item))
  return shards
}

function distributeByTime(
  items: SpecItem[],
  count: number,
  initial?: SpecItem[][],
): SpecItem[][] {
  const shards: SpecItem[][] = Array.from({ length: count }, (_, i) => [...(initial?.[i] ?? [])])
  const loads = shards.map(shard => shard.reduce((total, item) => total + item.duration, 0))
  for (const item of sortByDuration(items)) {
    let target = 0
    for (let i = 1; i < count; i++) {
      if (loads[i] < loads[target]) {
        target = i
      }
    }
    shards[target].push(item)
    loads[target] += item.duration
  }
  return shards
}

function distributeRoundRobin(items: SpecItem[], count: number): SpecItem[][] {
  const shards: SpecItem[][] = Array.from({ length: count }, () => [])
  let pointer = 0
  let direction = 1
  for (const item of sortByDuration(items)) {
    shards[pointer].push(item)
    pointer += direction
    if (pointer >= count || pointer < 0) {
      pointer = pointer < 0 ? 0 : count - 1
      direction = -direction
    }
  }
  return shards
}
