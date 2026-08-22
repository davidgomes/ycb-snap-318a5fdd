import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { ResolvedConfig, ShardStrategy } from '../types/config'
import type { TestSequencer } from './types'
import { slash } from '@vitest/utils/helpers'
import { relative, resolve } from 'pathe'
import { hash } from '../hash'
import { readDurationHistory } from './duration-history'
import { anyRuleMatches, matchAffinityShard } from './shard-affinity'
import { warnRebalanceThreshold } from './shard-analytics'

interface FileEntry {
  spec: TestSpecification
  path: string
  duration: number
}

type ShardAssignments = FileEntry[][]

export class BaseSequencer implements TestSequencer {
  protected ctx: Vitest

  constructor(ctx: Vitest) {
    this.ctx = ctx
  }

  public async shard(files: TestSpecification[]): Promise<TestSpecification[]> {
    const { config } = this.ctx
    const { index, count } = config.shard!
    const sequence = config.sequence

    const entries = files.map(spec => ({
      spec,
      path: getSpecPath(spec, config.root),
      duration: 0,
    }))

    const history = readDurationHistory(
      config.root,
      sequence.durationHistoryPath,
      sequence.durationHistoryTTL,
      sequence.durationSmoothing,
    )

    if (history) {
      for (const entry of entries) {
        entry.duration = history.get(entry.path) ?? 0
      }
    }

    const effectiveStrategy = history === null
      ? sequence.durationFallbackStrategy === 'equal-split' ? 'equal-split' as const : 'hash' as const
      : sequence.shardStrategy

    const assignments = sequence.isolateSlowThreshold > 0
      ? assignWithSlowIsolation(entries, count, sequence.isolateSlowThreshold, effectiveStrategy, sequence.shardAffinityRules, config.root)
      : assignByStrategy(entries, count, effectiveStrategy, sequence.shardAffinityRules, config.root)

    if (sequence.rebalanceThreshold > 0) {
      const loads = assignments.map(shard => shard.reduce((sum, file) => sum + file.duration, 0))
      warnRebalanceThreshold(this.ctx.logger, loads, sequence.rebalanceThreshold)
    }

    return assignments[index - 1].map(entry => entry.spec)
  }

  public async sort(files: TestSpecification[]): Promise<TestSpecification[]> {
    const cache = this.ctx.cache
    const { sequence, root } = this.ctx.config

    let durationMap: Map<string, number> | null = null
    if (sequence.durationBasedSorting) {
      durationMap = readDurationHistory(
        root,
        sequence.durationHistoryPath,
        sequence.durationHistoryTTL,
        sequence.durationSmoothing,
      )
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

      if (durationMap) {
        const pathA = getSpecPath(a, root)
        const pathB = getSpecPath(b, root)
        const durationA = durationMap.get(pathA)
        const durationB = durationMap.get(pathB)
        const hasA = durationA !== undefined
        const hasB = durationB !== undefined

        if (hasA && hasB) {
          const diff = durationB! - durationA!
          if (diff !== 0) {
            return diff
          }
        }
        else if (hasA !== hasB) {
          return hasA ? -1 : 1
        }
      }

      const keyA = `${a.project.name}:${relative(this.ctx.config.root, a.moduleId)}`
      const keyB = `${b.project.name}:${relative(this.ctx.config.root, b.moduleId)}`

      const aState = cache.getFileTestResults(keyA)
      const bState = cache.getFileTestResults(keyB)

      if (!aState || !bState) {
        const statsA = cache.getFileStats(keyA)
        const statsB = cache.getFileStats(keyB)

        if (!statsA || !statsB) {
          return !statsA && statsB ? -1 : !statsB && statsA ? 1 : 0
        }

        return statsB.size - statsA.size
      }

      if (aState.failed && !bState.failed) {
        return -1
      }
      if (!aState.failed && bState.failed) {
        return 1
      }

      return bState.duration - aState.duration
    })
  }

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

  protected shardByHash(entries: FileEntry[], shardIndex: number, shardCount: number, root: string): FileEntry[] {
    const sorted = [...entries]
      .map(entry => ({
        entry,
        hash: hash('sha1', getHashSpecPath(entry.spec, root), 'hex'),
      }))
      .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))

    const [shardStart, shardEnd] = this.calculateShardRange(sorted.length, shardIndex, shardCount)
    return sorted.slice(shardStart, shardEnd).map(({ entry }) => entry)
  }
}

function getSpecPath(spec: TestSpecification, root: string): string {
  const fullPath = resolve(slash(root), slash(spec.moduleId))
  return slash(fullPath.slice(root.length)).replace(/^\//, '')
}

function getHashSpecPath(spec: TestSpecification, root: string): string {
  const fullPath = resolve(slash(root), slash(spec.moduleId))
  return fullPath.slice(root.length)
}

function createEmptyAssignments(shardCount: number): ShardAssignments {
  return Array.from({ length: shardCount }, () => [])
}

function assignByStrategy(
  entries: FileEntry[],
  shardCount: number,
  strategy: ShardStrategy | 'equal-split',
  affinityRules: ResolvedConfig['sequence']['shardAffinityRules'],
  root: string,
): ShardAssignments {
  switch (strategy) {
    case 'hash':
      return assignHash(entries, shardCount, root)
    case 'equal-split':
      return assignEqualSplit(entries, shardCount)
    case 'time':
      return assignLPT(entries, shardCount)
    case 'round-robin':
      return assignRoundRobin(entries, shardCount)
    case 'affinity':
      return assignAffinity(entries, shardCount, affinityRules)
  }
}

function assignHash(entries: FileEntry[], shardCount: number, root: string): ShardAssignments {
  const sorted = [...entries]
    .map(entry => ({
      entry,
      hash: hash('sha1', getHashSpecPath(entry.spec, root), 'hex'),
    }))
    .sort((a, b) => (a.hash < b.hash ? -1 : a.hash > b.hash ? 1 : 0))

  const assignments = createEmptyAssignments(shardCount)
  const ranges = Array.from({ length: shardCount }, (_, i) => {
    const baseShardSize = Math.floor(sorted.length / shardCount)
    const remainder = sorted.length % shardCount
    if (remainder >= i + 1) {
      const shardSize = baseShardSize + 1
      return [shardSize * i, shardSize * (i + 1)] as const
    }
    const shardStart = remainder * (baseShardSize + 1) + (i - remainder) * baseShardSize
    return [shardStart, shardStart + baseShardSize] as const
  })

  for (let i = 0; i < shardCount; i++) {
    assignments[i] = sorted.slice(ranges[i][0], ranges[i][1]).map(({ entry }) => entry)
  }

  return assignments
}

function assignEqualSplit(entries: FileEntry[], shardCount: number): ShardAssignments {
  const assignments = createEmptyAssignments(shardCount)
  const sorted = [...entries].sort((a, b) => a.path.localeCompare(b.path))

  sorted.forEach((entry, index) => {
    assignments[index % shardCount].push(entry)
  })

  return assignments
}

function assignLPT(entries: FileEntry[], shardCount: number, initialLoads?: number[]): ShardAssignments {
  const assignments = createEmptyAssignments(shardCount)
  const loads = initialLoads ? [...initialLoads] : Array.from({ length: shardCount }, () => 0)

  const sorted = sortByDurationDesc(entries)

  for (const entry of sorted) {
    let minShard = 0
    for (let i = 1; i < shardCount; i++) {
      if (loads[i] < loads[minShard]) {
        minShard = i
      }
    }
    assignments[minShard].push(entry)
    loads[minShard] += entry.duration
  }

  return assignments
}

function assignRoundRobin(entries: FileEntry[], shardCount: number): ShardAssignments {
  const assignments = createEmptyAssignments(shardCount)
  const sorted = sortByDurationDesc(entries)

  let pointer = 0
  let direction = 1

  for (const entry of sorted) {
    assignments[pointer].push(entry)
    const next = pointer + direction
    if (next < 0 || next >= shardCount) {
      pointer = next < 0 ? 0 : shardCount - 1
      direction = -direction
    }
    else {
      pointer = next
    }
  }

  return assignments
}

function assignAffinity(
  entries: FileEntry[],
  shardCount: number,
  rules: ResolvedConfig['sequence']['shardAffinityRules'],
): ShardAssignments {
  const paths = entries.map(entry => entry.path)
  if (!anyRuleMatches(paths, rules)) {
    return assignLPT(entries, shardCount)
  }

  const assignments = createEmptyAssignments(shardCount)
  const loads = Array.from({ length: shardCount }, () => 0)
  const unmatched: FileEntry[] = []

  for (const entry of entries) {
    const shard = matchAffinityShard(entry.path, rules, shardCount)
    if (shard === undefined) {
      unmatched.push(entry)
    }
    else {
      assignments[shard].push(entry)
      loads[shard] += entry.duration
    }
  }

  if (unmatched.length > 0) {
    const lptAssignments = assignLPT(unmatched, shardCount, loads)
    for (let i = 0; i < shardCount; i++) {
      assignments[i].push(...lptAssignments[i])
    }
  }

  return assignments
}

function assignWithSlowIsolation(
  entries: FileEntry[],
  shardCount: number,
  threshold: number,
  strategy: ShardStrategy | 'equal-split',
  affinityRules: ResolvedConfig['sequence']['shardAffinityRules'],
  root: string,
): ShardAssignments {
  const slow = sortByDurationDesc(entries.filter(entry => entry.duration > threshold))
  const remaining = entries.filter(entry => entry.duration <= threshold)
  const assignments = createEmptyAssignments(shardCount)
  const loads = Array.from({ length: shardCount }, () => 0)

  for (let i = 0; i < Math.min(slow.length, shardCount); i++) {
    assignments[i].push(slow[i])
    loads[i] += slow[i].duration
  }

  if (slow.length >= shardCount) {
    for (let i = shardCount; i < slow.length; i++) {
      assignments[shardCount - 1].push(slow[i])
      loads[shardCount - 1] += slow[i].duration
    }
    assignments[shardCount - 1].push(...remaining)
    return assignments
  }

  const remainingAssignments = assignByStrategy(remaining, shardCount, strategy, affinityRules, root)
  for (let i = 0; i < shardCount; i++) {
    assignments[i].push(...remainingAssignments[i])
  }

  return assignments
}

function sortByDurationDesc(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => {
    if (b.duration !== a.duration) {
      return b.duration - a.duration
    }
    return a.path.localeCompare(b.path)
  })
}
