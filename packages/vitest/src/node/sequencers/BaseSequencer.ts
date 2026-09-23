import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { getSmoothedDurations, historyKey, readDurationHistory, writeDurationHistory } from './duration-history'
import { matchAffinity } from './shard-affinity'
import { computeLoadRatio, lptAssign, roundRobinAssign } from './shard-analytics'

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
    const strategy = sequence.shardStrategy ?? 'hash'
    const needsDurations = strategy !== 'hash' || (sequence.isolateSlowThreshold ?? 0) > 0
    if (!needsDurations) {
      return this.hashShard(files, index, count)
    }
    const durations = this.loadDurations()
    if (!durations) {
      if (sequence.durationFallbackStrategy === 'equal-split') {
        return [...files]
          .map(spec => ({ spec, key: historyKey(config.root, spec.moduleId) }))
          .sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0))
          .filter((_, i) => (i % count) + 1 === index)
          .map(({ spec }) => spec)
      }
      return this.hashShard(files, index, count)
    }

    const items = files.map((spec, i) => {
      const path = historyKey(config.root, spec.moduleId)
      return { spec, path, key: `${i}:${path}`, duration: durations.get(path) ?? 0 }
    })
    const assignment = new Map<string, number>()
    const loads: number[] = Array.from({ length: count }, () => 0)

    let pool = items
    let shardOffset = 0
    let shardCount = count
    const threshold = sequence.isolateSlowThreshold ?? 0
    if (threshold > 0) {
      const slow = items.filter(i => i.duration > threshold).sort((a, b) => b.duration - a.duration)
      const rest = items.filter(i => i.duration <= threshold)
      if (slow.length >= count) {
        slow.forEach((item, i) => {
          const target = Math.min(i, count - 1)
          assignment.set(item.key, target)
          loads[target] += item.duration
        })
        for (const item of rest) {
          assignment.set(item.key, count - 1)
          loads[count - 1] += item.duration
        }
        pool = []
      }
      else if (slow.length) {
        slow.forEach((item, i) => {
          assignment.set(item.key, i)
          loads[i] += item.duration
        })
        pool = rest
        shardOffset = slow.length
        shardCount = count - slow.length
      }
    }

    if (pool.length) {
      const subLoads = loads.slice(shardOffset)
      const subAssignment = new Map<string, number>()
      this.assignByStrategy(strategy, pool, shardCount, subLoads, subAssignment)
      for (const [key, shard] of subAssignment) {
        assignment.set(key, shard + shardOffset)
      }
      subLoads.forEach((load, i) => (loads[shardOffset + i] = load))
    }

    const rebalance = sequence.rebalanceThreshold ?? 0
    if (rebalance > 0) {
      const ratio = computeLoadRatio(loads)
      if (ratio < rebalance) {
        this.ctx.logger.warn(
          `Shard load imbalance detected: ratio=${ratio.toFixed(2)} is below threshold=${rebalance.toFixed(2)}`,
        )
      }
    }

    return items
      .filter(item => assignment.get(item.key) === index - 1)
      .map(item => item.spec)
  }

  private assignByStrategy(
    strategy: string,
    items: { key: string; path: string; duration: number }[],
    count: number,
    loads: number[],
    assignment: Map<string, number>,
  ): void {
    if (strategy === 'round-robin') {
      roundRobinAssign(items.map(i => ({ key: i.key, duration: i.duration })), count, loads, assignment)
      return
    }
    if (strategy === 'affinity') {
      const rules = this.ctx.config.sequence.shardAffinityRules ?? []
      const unmatched: typeof items = []
      for (const item of items) {
        const target = matchAffinity(item.path, rules, count)
        if (target === undefined) {
          unmatched.push(item)
        }
        else {
          assignment.set(item.key, target)
          loads[target] += item.duration
        }
      }
      lptAssign(unmatched, count, loads, assignment)
      return
    }
    lptAssign(items, count, loads, assignment)
  }

  private loadDurations(): Map<string, number> | null {
    const { config } = this.ctx
    const history = readDurationHistory({
      root: config.root,
      path: config.sequence.durationHistoryPath ?? 'duration-history.json',
      ttl: config.sequence.durationHistoryTTL ?? 0,
    })
    if (!history) {
      return null
    }
    return getSmoothedDurations(history, config.sequence.durationSmoothing ?? 'latest')
  }

  public async recordFileDurations(): Promise<void> {
    const { config } = this.ctx
    if (!config.sequence.recordFileDurations) {
      return
    }
    const durations = new Map<string, number>()
    for (const file of this.ctx.state.getFiles()) {
      const duration = file.result?.duration
      if (typeof duration === 'number' && Number.isFinite(duration)) {
        durations.set(historyKey(config.root, file.filepath), duration)
      }
    }
    if (!durations.size) {
      return
    }
    await writeDurationHistory({
      root: config.root,
      path: config.sequence.durationHistoryPath ?? 'duration-history.json',
      ttl: config.sequence.durationHistoryTTL ?? 0,
      maxRuns: config.sequence.durationHistoryMaxRuns ?? 1,
    }, durations)
  }

  private hashShard(files: TestSpecification[], index: number, count: number): TestSpecification[] {
    const { config } = this.ctx
    const [shardStart, shardEnd] = this.calculateShardRange(files.length, index, count)
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
      .slice(shardStart, shardEnd)
      .map(({ spec }) => spec)
  }

  // async so it can be extended by other sequelizers
  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    if (this.ctx.config.sequence.durationBasedSorting) {
      const durations = this.loadDurations() ?? new Map<string, number>()
      const root = this.ctx.config.root
      return [...files].sort((a, b) => {
        const da = durations.get(historyKey(root, a.moduleId))
        const db = durations.get(historyKey(root, b.moduleId))
        if (da === undefined || db === undefined) {
          return da === undefined && db !== undefined ? 1 : db === undefined && da !== undefined ? -1 : 0
        }
        return db - da
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
