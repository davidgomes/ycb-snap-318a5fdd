import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import c from 'tinyrainbow'
import { hash } from '../hash'
import { getDurationHistoryKey, readFileDurations, resolveDurationHistoryPath } from './duration-history'
import { createShardAffinityMatcher } from './shard-affinity'
import { formatShardImbalanceWarning, getShardBalance, getShardLoads } from './shard-analytics'

interface ShardEntry {
  spec: TestSpecification
  key: string
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
    const { shardStrategy, isolateSlowThreshold, rebalanceThreshold, durationFallbackStrategy } = config.sequence

    const usesDurations = shardStrategy !== 'hash' || isolateSlowThreshold > 0 || rebalanceThreshold > 0
    const durations = usesDurations ? await this.readFileDurations() : null

    if (!durations) {
      if (shardStrategy !== 'hash' && durationFallbackStrategy === 'equal-split') {
        return this.shardByEqualSplit(files, index, count)
      }
      return this.shardByHash(files, index, count)
    }

    const entries = files.map<ShardEntry>((spec) => {
      const key = getDurationHistoryKey(config.root, spec.moduleId)
      return { spec, key, duration: durations.get(key) ?? 0 }
    })
    const groups = this.partitionWithIsolation(entries, count)

    if (rebalanceThreshold > 0) {
      const balance = getShardBalance(getShardLoads(groups, entry => entry.duration))
      if (balance && balance.ratio < rebalanceThreshold) {
        this.ctx.logger.warn(c.yellow(formatShardImbalanceWarning(balance, rebalanceThreshold)))
      }
    }

    return groups[index - 1].map(({ spec }) => spec)
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    const cache = this.ctx.cache
    const durations = this.ctx.config.sequence.durationBasedSorting
      ? await this.readFileDurations()
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

      // Isolated run first
      if (a.project.config.isolate && !b.project.config.isolate) {
        return -1
      }
      if (!a.project.config.isolate && b.project.config.isolate) {
        return 1
      }

      if (durations) {
        const durationA = durations.get(getDurationHistoryKey(this.ctx.config.root, a.moduleId))
        const durationB = durations.get(getDurationHistoryKey(this.ctx.config.root, b.moduleId))
        if (durationA !== undefined && durationB !== undefined) {
          return durationB - durationA
        }
        // files without recorded duration run last
        if (durationA !== undefined) {
          return -1
        }
        if (durationB !== undefined) {
          return 1
        }
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

  private async readFileDurations(): Promise<Map<string, number> | null> {
    const { root, sequence } = this.ctx.config
    return readFileDurations(
      resolveDurationHistoryPath(root, sequence.durationHistoryPath),
      { ttl: sequence.durationHistoryTTL, smoothing: sequence.durationSmoothing },
    )
  }

  private sortByHash<T extends { spec: TestSpecification }>(entries: T[]): T[] {
    const root = this.ctx.config.root
    return entries
      .map((entry) => {
        const fullPath = resolve(slash(root), slash(entry.spec.moduleId))
        const specPath = fullPath?.slice(root.length)
        return {
          entry,
          hash: hash('sha1', specPath, 'hex'),
        }
      })
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))
      .map(({ entry }) => entry)
  }

  private shardByHash(files: TestSpecification[], index: number, count: number): TestSpecification[] {
    const [shardStart, shardEnd] = this.calculateShardRange(files.length, index, count)
    return this.sortByHash(files.map(spec => ({ spec })))
      .slice(shardStart, shardEnd)
      .map(({ spec }) => spec)
  }

  private shardByEqualSplit(files: TestSpecification[], index: number, count: number): TestSpecification[] {
    const root = this.ctx.config.root
    return files
      .map(spec => ({ spec, key: getDurationHistoryKey(root, spec.moduleId) }))
      .sort((a, b) => comparePaths(a.key, b.key))
      .filter((_, i) => (i % count) + 1 === index)
      .map(({ spec }) => spec)
  }

  private partitionWithIsolation(entries: ShardEntry[], count: number): ShardEntry[][] {
    const threshold = this.ctx.config.sequence.isolateSlowThreshold
    const slow = threshold > 0
      ? sortByDuration(entries.filter(entry => entry.duration > threshold))
      : []
    if (!slow.length) {
      return this.partition(entries, count)
    }

    const remaining = entries.filter(entry => entry.duration <= threshold)
    const groups = createGroups<ShardEntry>(count)
    if (slow.length >= count) {
      slow.forEach((entry, i) => groups[Math.min(i, count - 1)].push(entry))
      groups[count - 1].push(...remaining)
      return groups
    }

    slow.forEach((entry, i) => groups[i].push(entry))
    this.partition(remaining, count - slow.length).forEach((group, i) => {
      groups[slow.length + i].push(...group)
    })
    return groups
  }

  private partition(entries: ShardEntry[], count: number): ShardEntry[][] {
    switch (this.ctx.config.sequence.shardStrategy) {
      case 'hash':
        return this.partitionByHash(entries, count)
      case 'time':
        return partitionByTime(entries, createGroups(count))
      case 'round-robin':
        return partitionByRoundRobin(entries, count)
      case 'affinity':
        return this.partitionByAffinity(entries, count)
    }
  }

  private partitionByHash(entries: ShardEntry[], count: number): ShardEntry[][] {
    const sorted = this.sortByHash(entries)
    return createGroups<ShardEntry>(count).map((_, i) => {
      const [start, end] = this.calculateShardRange(sorted.length, i + 1, count)
      return sorted.slice(start, end)
    })
  }

  private partitionByAffinity(entries: ShardEntry[], count: number): ShardEntry[][] {
    const match = createShardAffinityMatcher(this.ctx.config.sequence.shardAffinityRules, count)
    const groups = createGroups<ShardEntry>(count)
    const unmatched: ShardEntry[] = []
    for (const entry of entries) {
      const shardIndex = match(entry.key)
      if (shardIndex === undefined) {
        unmatched.push(entry)
      }
      else {
        groups[shardIndex].push(entry)
      }
    }
    return partitionByTime(unmatched, groups)
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

function createGroups<T>(count: number): T[][] {
  return Array.from({ length: count }, () => [])
}

function comparePaths(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

function sortByDuration(entries: ShardEntry[]): ShardEntry[] {
  return [...entries].sort((a, b) => b.duration - a.duration || comparePaths(a.key, b.key))
}

// longest processing time first: the next longest file goes to the least loaded shard
function partitionByTime(entries: ShardEntry[], groups: ShardEntry[][]): ShardEntry[][] {
  const loads = getShardLoads(groups, entry => entry.duration)
  for (const entry of sortByDuration(entries)) {
    let target = 0
    for (let i = 1; i < loads.length; i++) {
      if (loads[i] < loads[target]) {
        target = i
      }
    }
    groups[target].push(entry)
    loads[target] += entry.duration
  }
  return groups
}

function partitionByRoundRobin(entries: ShardEntry[], count: number): ShardEntry[][] {
  const groups = createGroups<ShardEntry>(count)
  let pointer = 0
  let direction = 1
  for (const entry of sortByDuration(entries)) {
    groups[pointer].push(entry)
    const next = pointer + direction
    if (next < 0 || next >= count) {
      pointer = next < 0 ? 0 : count - 1
      direction = -direction
    }
    else {
      pointer = next
    }
  }
  return groups
}
