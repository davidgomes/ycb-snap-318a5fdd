import type {
  DurationFallbackStrategy,
  DurationSmoothing,
  SequenceOptions,
  ShardAffinityRule,
  ShardStrategy,
} from '../types/config'

export interface ResolvedSequenceShardConfig {
  shardStrategy: ShardStrategy
  balanceShardsByTime: boolean
  recordFileDurations: boolean
  durationBasedSorting: boolean
  durationHistoryTTL: number
  durationHistoryPath: string
  durationHistoryMaxRuns: number
  durationSmoothing: DurationSmoothing
  shardAffinityRules: ShardAffinityRule[]
  rebalanceThreshold: number
  isolateSlowThreshold: number
  durationFallbackStrategy: DurationFallbackStrategy
}

const SHARD_STRATEGIES = ['hash', 'time', 'round-robin', 'affinity'] as const
const DURATION_SMOOTHING = ['latest', 'average', 'p95', 'median'] as const
const DURATION_FALLBACKS = ['hash', 'equal-split'] as const

function formatReceived(value: unknown): string {
  if (typeof value === 'string') {
    return JSON.stringify(value)
  }
  if (typeof value === 'number' && !Number.isFinite(value)) {
    return String(value)
  }
  if (typeof value === 'bigint' || typeof value === 'symbol' || typeof value === 'function') {
    return String(value)
  }
  try {
    const serialized = JSON.stringify(value)
    if (serialized !== undefined) {
      return serialized
    }
  }
  catch {
    // Circular values fall through to String().
  }
  return String(value)
}

function invalid(field: string, message: string): never {
  throw new TypeError(`Invalid sequence.${field}: ${message}`)
}

function assertOneOf<T extends string>(value: unknown, field: string, allowed: readonly T[]): asserts value is T {
  if (typeof value !== 'string' || !(allowed as readonly string[]).includes(value)) {
    const expected = allowed.map(item => `"${item}"`).join(', ')
    invalid(field, `expected ${expected}, received ${formatReceived(value)}`)
  }
}

function optionalOneOf<T extends string>(value: unknown, field: string, allowed: readonly T[]): T | undefined {
  if (value === undefined) {
    return undefined
  }
  assertOneOf(value, field, allowed)
  return value
}

function optionalBoolean(value: unknown, field: string): boolean | undefined {
  if (value === undefined) {
    return undefined
  }
  if (typeof value !== 'boolean') {
    invalid(field, `expected a boolean, received ${formatReceived(value)}`)
  }
  return value
}

function optionalNumber(
  value: unknown,
  field: string,
  check: (value: number) => boolean,
  message: string,
): number | undefined {
  if (value === undefined) {
    return undefined
  }
  if (typeof value !== 'number' || !check(value)) {
    invalid(field, `${message}, received ${formatReceived(value)}`)
  }
  return value
}

function optionalString(value: unknown, field: string, check: (value: string) => boolean, message: string): string | undefined {
  if (value === undefined) {
    return undefined
  }
  if (typeof value !== 'string' || !check(value)) {
    invalid(field, message)
  }
  return value
}

export function resolveSequenceOptions(
  sequence: { [Key in keyof SequenceOptions]?: unknown } = {},
): ResolvedSequenceShardConfig {
  const providedStrategy = optionalOneOf(sequence.shardStrategy, 'shardStrategy', SHARD_STRATEGIES)
  const providedBalance = optionalBoolean(sequence.balanceShardsByTime, 'balanceShardsByTime')
  const recordFileDurations = optionalBoolean(sequence.recordFileDurations, 'recordFileDurations') ?? false
  const durationBasedSorting = optionalBoolean(sequence.durationBasedSorting, 'durationBasedSorting') ?? false
  const durationHistoryTTL = optionalNumber(
    sequence.durationHistoryTTL,
    'durationHistoryTTL',
    value => Number.isFinite(value) && value >= 0,
    'expected a finite number greater than or equal to 0',
  ) ?? 0
  const durationHistoryPath = optionalString(
    sequence.durationHistoryPath,
    'durationHistoryPath',
    value => value.length > 0 && value === value.trim(),
    'expected a non-empty string with no leading or trailing whitespace',
  ) ?? 'duration-history.json'
  const durationHistoryMaxRuns = optionalNumber(
    sequence.durationHistoryMaxRuns,
    'durationHistoryMaxRuns',
    value => Number.isInteger(value) && value >= 1,
    'expected an integer greater than or equal to 1',
  ) ?? 1
  const durationSmoothing = optionalOneOf(sequence.durationSmoothing, 'durationSmoothing', DURATION_SMOOTHING) ?? 'latest'
  const rebalanceThreshold = optionalNumber(
    sequence.rebalanceThreshold,
    'rebalanceThreshold',
    value => Number.isFinite(value) && value >= 0 && value <= 1,
    'expected a number between 0 and 1',
  ) ?? 0
  const isolateSlowThreshold = optionalNumber(
    sequence.isolateSlowThreshold,
    'isolateSlowThreshold',
    value => Number.isFinite(value) && value >= 0,
    'expected a finite number greater than or equal to 0',
  ) ?? 0
  const durationFallbackStrategy = optionalOneOf(
    sequence.durationFallbackStrategy,
    'durationFallbackStrategy',
    DURATION_FALLBACKS,
  ) ?? 'hash'
  const shardAffinityRules = resolveAffinityRules(sequence.shardAffinityRules)

  let balanceShardsByTime = providedBalance ?? false
  const shardStrategy: ShardStrategy = providedStrategy === undefined && balanceShardsByTime
    ? 'time'
    : providedStrategy ?? 'hash'
  if (shardStrategy !== 'time') {
    balanceShardsByTime = false
  }

  return {
    shardStrategy,
    balanceShardsByTime,
    recordFileDurations,
    durationBasedSorting,
    durationHistoryTTL,
    durationHistoryPath,
    durationHistoryMaxRuns,
    durationSmoothing,
    shardAffinityRules,
    rebalanceThreshold,
    isolateSlowThreshold,
    durationFallbackStrategy,
  }
}

function resolveAffinityRules(rules: unknown): ShardAffinityRule[] {
  if (rules === undefined) {
    return []
  }
  if (!Array.isArray(rules)) {
    invalid('shardAffinityRules', `expected an array, received ${formatReceived(rules)}`)
  }

  return rules.map((rule, index) => {
    if (rule == null || typeof rule !== 'object' || Array.isArray(rule)) {
      invalid(`shardAffinityRules[${index}]`, 'expected an object with pattern and shardIndex')
    }
    const candidate = rule as { pattern?: unknown; shardIndex?: unknown }
    if (typeof candidate.pattern !== 'string' || candidate.pattern.length === 0) {
      invalid(`shardAffinityRules[${index}].pattern`, 'expected a non-empty string')
    }
    if (typeof candidate.shardIndex !== 'number' || !Number.isInteger(candidate.shardIndex) || candidate.shardIndex < 0) {
      invalid(
        `shardAffinityRules[${index}].shardIndex`,
        `expected an integer greater than or equal to 0, received ${formatReceived(candidate.shardIndex)}`,
      )
    }
    return {
      pattern: candidate.pattern,
      shardIndex: candidate.shardIndex,
    }
  })
}
