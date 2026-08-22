import type { SequenceOptions, ShardAffinityRule } from '../types/config'

const SHARD_STRATEGIES = ['hash', 'time', 'round-robin', 'affinity'] as const
const DURATION_SMOOTHINGS = ['latest', 'average', 'p95', 'median'] as const
const DURATION_FALLBACK_STRATEGIES = ['hash', 'equal-split'] as const

export interface ResolvedSequenceShardingOptions {
  shardStrategy: typeof SHARD_STRATEGIES[number]
  balanceShardsByTime: boolean
  recordFileDurations: boolean
  durationBasedSorting: boolean
  durationHistoryTTL: number
  durationHistoryPath: string
  durationHistoryMaxRuns: number
  durationSmoothing: typeof DURATION_SMOOTHINGS[number]
  shardAffinityRules: ShardAffinityRule[]
  rebalanceThreshold: number
  isolateSlowThreshold: number
  durationFallbackStrategy: typeof DURATION_FALLBACK_STRATEGIES[number]
}

function validateShardAffinityRules(rules: unknown): asserts rules is ShardAffinityRule[] {
  if (!Array.isArray(rules)) {
    throw new TypeError('sequence.shardAffinityRules must be an array')
  }

  for (const rule of rules) {
    if (typeof rule !== 'object' || rule === null) {
      throw new Error('sequence.shardAffinityRules entries must be objects with "pattern" and "shardIndex"')
    }
    if (typeof rule.pattern !== 'string') {
      throw new TypeError('sequence.shardAffinityRules entries must have a string "pattern"')
    }
    if (typeof rule.shardIndex !== 'number' || !Number.isInteger(rule.shardIndex) || rule.shardIndex < 0) {
      throw new Error('sequence.shardAffinityRules entries must have an integer "shardIndex" >= 0')
    }
  }
}

export function resolveSequenceShardingOptions(
  sequence: SequenceOptions,
): ResolvedSequenceShardingOptions {
  const shardStrategyExplicit = sequence.shardStrategy !== undefined

  if (sequence.shardStrategy !== undefined && !SHARD_STRATEGIES.includes(sequence.shardStrategy)) {
    throw new Error(`sequence.shardStrategy must be one of ${SHARD_STRATEGIES.map(s => `'${s}'`).join(', ')}`)
  }

  if (sequence.balanceShardsByTime !== undefined && typeof sequence.balanceShardsByTime !== 'boolean') {
    throw new Error('sequence.balanceShardsByTime must be a boolean')
  }

  if (sequence.recordFileDurations !== undefined && typeof sequence.recordFileDurations !== 'boolean') {
    throw new Error('sequence.recordFileDurations must be a boolean')
  }

  if (sequence.durationBasedSorting !== undefined && typeof sequence.durationBasedSorting !== 'boolean') {
    throw new Error('sequence.durationBasedSorting must be a boolean')
  }

  if (sequence.durationHistoryTTL !== undefined) {
    if (typeof sequence.durationHistoryTTL !== 'number' || !Number.isFinite(sequence.durationHistoryTTL) || sequence.durationHistoryTTL < 0) {
      throw new Error('sequence.durationHistoryTTL must be a finite number >= 0')
    }
  }

  if (sequence.durationHistoryPath !== undefined) {
    if (typeof sequence.durationHistoryPath !== 'string' || sequence.durationHistoryPath.length === 0) {
      throw new Error('sequence.durationHistoryPath must be a non-empty string')
    }
    if (sequence.durationHistoryPath.trim() !== sequence.durationHistoryPath) {
      throw new Error('sequence.durationHistoryPath must not have leading or trailing whitespace')
    }
  }

  if (sequence.durationHistoryMaxRuns !== undefined) {
    if (typeof sequence.durationHistoryMaxRuns !== 'number' || !Number.isInteger(sequence.durationHistoryMaxRuns) || sequence.durationHistoryMaxRuns < 1) {
      throw new Error('sequence.durationHistoryMaxRuns must be an integer >= 1')
    }
  }

  if (sequence.durationSmoothing !== undefined && !DURATION_SMOOTHINGS.includes(sequence.durationSmoothing)) {
    throw new Error(`sequence.durationSmoothing must be one of ${DURATION_SMOOTHINGS.map(s => `'${s}'`).join(', ')}`)
  }

  if (sequence.shardAffinityRules !== undefined) {
    validateShardAffinityRules(sequence.shardAffinityRules)
  }

  if (sequence.rebalanceThreshold !== undefined) {
    if (typeof sequence.rebalanceThreshold !== 'number' || !Number.isFinite(sequence.rebalanceThreshold) || sequence.rebalanceThreshold < 0 || sequence.rebalanceThreshold > 1) {
      throw new Error('sequence.rebalanceThreshold must be a number between 0 and 1 inclusive')
    }
  }

  if (sequence.isolateSlowThreshold !== undefined) {
    if (typeof sequence.isolateSlowThreshold !== 'number' || !Number.isFinite(sequence.isolateSlowThreshold) || sequence.isolateSlowThreshold < 0) {
      throw new Error('sequence.isolateSlowThreshold must be a number >= 0')
    }
  }

  if (sequence.durationFallbackStrategy !== undefined && !DURATION_FALLBACK_STRATEGIES.includes(sequence.durationFallbackStrategy)) {
    throw new Error(`sequence.durationFallbackStrategy must be one of ${DURATION_FALLBACK_STRATEGIES.map(s => `'${s}'`).join(', ')}`)
  }

  const balanceShardsByTime = sequence.balanceShardsByTime ?? false
  let shardStrategy = sequence.shardStrategy ?? 'hash'

  if (balanceShardsByTime && !shardStrategyExplicit) {
    shardStrategy = 'time'
  }

  return {
    shardStrategy,
    balanceShardsByTime: shardStrategy === 'time' ? balanceShardsByTime : false,
    recordFileDurations: sequence.recordFileDurations ?? false,
    durationBasedSorting: sequence.durationBasedSorting ?? false,
    durationHistoryTTL: sequence.durationHistoryTTL ?? 0,
    durationHistoryPath: sequence.durationHistoryPath ?? 'duration-history.json',
    durationHistoryMaxRuns: sequence.durationHistoryMaxRuns ?? 1,
    durationSmoothing: sequence.durationSmoothing ?? 'latest',
    shardAffinityRules: sequence.shardAffinityRules ?? [],
    rebalanceThreshold: sequence.rebalanceThreshold ?? 0,
    isolateSlowThreshold: sequence.isolateSlowThreshold ?? 0,
    durationFallbackStrategy: sequence.durationFallbackStrategy ?? 'hash',
  }
}
