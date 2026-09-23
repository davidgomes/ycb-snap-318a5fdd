import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { ShardStrategy } from '../types/config'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { getDurationHistoryKey, loadFileDurations } from './duration-history'
import { assignShardAffinity } from './shard-affinity'
import { getShardLoads, warnUnbalancedShards } from './shard-analytics'

interface ShardItem {
  spec: TestSpecification
  path: string
  duration: number
}

export class BaseSequencer implements TestSequencer {
  protected ctx: Vitest

  constructor(ctx: Vitest) {
    this.ctx = ctx
  }

  // async so it can be extended by other sequelizers
  public async shard(files: TestSpecification[]): Promise<TestSpecification[]> {
    const { config } = this.ctx
    const { index, count } = config.shard!
    const { sequence } = config
    const strategy = sequence.shardStrategy ?? (sequence.balanceShardsByTime ? 'time' : 'hash')
    const isolateSlowThreshold = sequence.isolateSlowThreshold ?? 0
    const rebalanceThreshold = sequence.rebalanceThreshold ?? 0

    const durations = strategy !== 'hash' || isolateSlowThreshold > 0 || rebalanceThreshold > 0
      ? await loadFileDurations(config)
      : null

    const items = files.map((spec): ShardItem => {
      const path = getDurationHistoryKey(config.root, spec.moduleId)
      return { spec, path, duration: durations?.get(path) ?? 0 }
    })

    let shards: ShardItem[][]
    if (durations) {
      shards = this.distributeByDuration(items, count, strategy, isolateSlowThreshold)
      warnUnbalancedShards(this.ctx.logger, getShardLoads(shards), rebalanceThreshold)
    }
    else if (strategy !== 'hash' && sequence.durationFallbackStrategy === 'equal-split') {
      shards = splitEqually(items, count)
    }
    else {
      shards = this.splitByHash(items, count)
    }

    return shards[index - 1].map(({ spec }) => spec)
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    if (this.ctx.config.sequence.durationBasedSorting) {
      const durations = await loadFileDurations(this.ctx.config)
      if (durations) {
        return this.sortByRecordedDuration(files, durations)
      }
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

  private sortByRecordedDuration(files: TestSpecification[], durations: Map<string, number>): TestSpecification[] {
    const { root } = this.ctx.config
    return files
      .map(spec => ({ spec, duration: durations.get(getDurationHistoryKey(root, spec.moduleId)) }))
      .sort((a, b) => {
        // files without recorded duration run last
        if (a.duration === undefined || b.duration === undefined) {
          return a.duration === b.duration ? 0 : a.duration === undefined ? 1 : -1
        }
        return b.duration - a.duration
      })
      .map(({ spec }) => spec)
  }

  private distributeByDuration(
    items: ShardItem[],
    count: number,
    strategy: ShardStrategy,
    isolateSlowThreshold: number,
  ): ShardItem[][] {
    if (isolateSlowThreshold > 0) {
      const slow = sortByDuration(items.filter(item => item.duration > isolateSlowThreshold))
      if (slow.length) {
        const remaining = items.filter(item => item.duration <= isolateSlowThreshold)
        if (slow.length >= count) {
          const shards = slow.slice(0, count - 1).map(item => [item])
          shards.push([...slow.slice(count - 1), ...remaining])
          return shards
        }
        return [
          ...slow.map(item => [item]),
          ...this.distributeByStrategy(remaining, count - slow.length, strategy),
        ]
      }
    }
    return this.distributeByStrategy(items, count, strategy)
  }

  private distributeByStrategy(items: ShardItem[], count: number, strategy: ShardStrategy): ShardItem[][] {
    switch (strategy) {
      case 'time':
        return assignByLoad(items, createShards(count))
      case 'round-robin':
        return assignRoundRobin(items, count)
      case 'affinity': {
        const rules = this.ctx.config.sequence.shardAffinityRules ?? []
        const assignment = assignShardAffinity(items, rules, count)
        return assignment
          ? assignByLoad(assignment.unmatched, assignment.shards)
          : assignByLoad(items, createShards(count))
      }
      default:
        return this.splitByHash(items, count)
    }
  }

  private splitByHash(items: ShardItem[], count: number): ShardItem[][] {
    const { root } = this.ctx.config
    const sorted = items
      .map((item) => {
        const fullPath = resolve(slash(root), slash(item.spec.moduleId))
        const specPath = fullPath?.slice(root.length)
        return {
          item,
          hash: hash('sha1', specPath, 'hex'),
        }
      })
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))
      .map(({ item }) => item)

    return Array.from({ length: count }, (_, shardIndex) => {
      const [shardStart, shardEnd] = this.calculateShardRange(sorted.length, shardIndex + 1, count)
      return sorted.slice(shardStart, shardEnd)
    })
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

function createShards(count: number): ShardItem[][] {
  return Array.from({ length: count }, () => [])
}

function comparePaths(a: ShardItem, b: ShardItem): number {
  return a.path < b.path ? -1 : a.path > b.path ? 1 : 0
}

function sortByDuration(items: ShardItem[]): ShardItem[] {
  return [...items].sort((a, b) => b.duration - a.duration || comparePaths(a, b))
}

function splitEqually(items: ShardItem[], count: number): ShardItem[][] {
  const shards = createShards(count)
  ;[...items].sort(comparePaths).forEach((item, index) => shards[index % count].push(item))
  return shards
}

// Longest processing time first: the slowest file goes to the least loaded shard
function assignByLoad(items: ShardItem[], shards: ShardItem[][]): ShardItem[][] {
  const loads = getShardLoads(shards)
  for (const item of sortByDuration(items)) {
    let target = 0
    for (let index = 1; index < loads.length; index++) {
      if (loads[index] < loads[target]) {
        target = index
      }
    }
    shards[target].push(item)
    loads[target] += item.duration
  }
  return shards
}

// Deals files back and forth, so boundary shards receive two files in a row
function assignRoundRobin(items: ShardItem[], count: number): ShardItem[][] {
  const shards = createShards(count)
  let pointer = 0
  let direction = 1
  for (const item of sortByDuration(items)) {
    shards[pointer].push(item)
    pointer += direction
    if (pointer < 0 || pointer >= count) {
      pointer = Math.min(Math.max(pointer, 0), count - 1)
      direction = -direction
    }
  }
  return shards
}
