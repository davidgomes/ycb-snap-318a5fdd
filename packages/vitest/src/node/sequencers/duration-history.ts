import type { DurationFallbackStrategy, DurationSmoothing, SequenceOptions, ShardAffinityRule, ShardStrategyName } from '../types/config'
import type { DurationObservation } from './duration-smoothing'
import fs from 'node:fs'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'

export interface DurationHistoryOptions {
  historyPath?: string
  ttl?: number
  now?: number
}

export interface DurationSequenceState {
  shardStrategy?: ShardStrategyName
  balanceShardsByTime?: boolean
  recordFileDurations?: boolean
  durationBasedSorting?: boolean
  durationHistoryTTL?: number
  durationHistoryPath?: string
  durationHistoryMaxRuns?: number
  durationSmoothing?: DurationSmoothing
  shardAffinityRules?: ShardAffinityRule[]
  rebalanceThreshold?: number
  isolateSlowThreshold?: number
  durationFallbackStrategy?: DurationFallbackStrategy
}

const SHARD_STRATEGIES = ['hash', 'time', 'round-robin', 'affinity'] as const
const SMOOTHING = ['latest', 'average', 'p95', 'median'] as const
const FALLBACKS = ['hash', 'equal-split'] as const

function invalid(field: string, message: string): TypeError {
  return new TypeError(`sequence.${field} ${message}`)
}

function assertEnum<T extends string>(value: unknown, allowed: readonly T[], field: string): T {
  if (typeof value !== 'string' || !(allowed as readonly string[]).includes(value)) {
    throw invalid(field, `must be one of: ${allowed.map(item => `'${item}'`).join(', ')}. Received ${JSON.stringify(value)}`)
  }
  return value as T
}

function assertBoolean(value: unknown, field: string): boolean {
  if (typeof value !== 'boolean') {
    throw invalid(field, `must be a boolean. Received ${JSON.stringify(value)}`)
  }
  return value
}

function assertFiniteNumber(value: unknown, field: string, extra?: (value: number) => string | undefined): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw invalid(field, `must be a finite number. Received ${JSON.stringify(value)}`)
  }
  const message = extra?.(value)
  if (message) {
    throw invalid(field, message)
  }
  return value
}

function assertInteger(value: unknown, field: string, min: number): number {
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw invalid(field, `must be an integer >= ${min}. Received ${JSON.stringify(value)}`)
  }
  if (value < min) {
    throw invalid(field, `must be an integer >= ${min}. Received ${JSON.stringify(value)}`)
  }
  return value
}

export function resolveDurationSequence(sequence: DurationSequenceState & SequenceOptions): void {
  const strategyProvided = sequence.shardStrategy !== undefined
  if (strategyProvided) {
    sequence.shardStrategy = assertEnum(sequence.shardStrategy, SHARD_STRATEGIES, 'shardStrategy')
  }

  if (sequence.balanceShardsByTime !== undefined) {
    sequence.balanceShardsByTime = assertBoolean(sequence.balanceShardsByTime, 'balanceShardsByTime')
  }
  else {
    sequence.balanceShardsByTime = false
  }

  if (sequence.recordFileDurations !== undefined) {
    sequence.recordFileDurations = assertBoolean(sequence.recordFileDurations, 'recordFileDurations')
  }
  else {
    sequence.recordFileDurations = false
  }

  if (sequence.durationBasedSorting !== undefined) {
    sequence.durationBasedSorting = assertBoolean(sequence.durationBasedSorting, 'durationBasedSorting')
  }
  else {
    sequence.durationBasedSorting = false
  }

  if (sequence.durationHistoryTTL !== undefined) {
    sequence.durationHistoryTTL = assertFiniteNumber(
      sequence.durationHistoryTTL,
      'durationHistoryTTL',
      value => value < 0 ? `must be a finite number >= 0. Received ${value}` : undefined,
    )
  }
  else {
    sequence.durationHistoryTTL = 0
  }

  if (sequence.durationHistoryPath !== undefined) {
    const historyPath = sequence.durationHistoryPath
    if (typeof historyPath !== 'string' || historyPath.length === 0 || historyPath !== historyPath.trim()) {
      throw invalid(
        'durationHistoryPath',
        `must be a non-empty string with no leading or trailing whitespace. Received ${JSON.stringify(historyPath)}`,
      )
    }
  }
  else {
    sequence.durationHistoryPath = 'duration-history.json'
  }

  if (sequence.durationHistoryMaxRuns !== undefined) {
    sequence.durationHistoryMaxRuns = assertInteger(sequence.durationHistoryMaxRuns, 'durationHistoryMaxRuns', 1)
  }
  else {
    sequence.durationHistoryMaxRuns = 1
  }

  if (sequence.durationSmoothing !== undefined) {
    sequence.durationSmoothing = assertEnum(sequence.durationSmoothing, SMOOTHING, 'durationSmoothing')
  }
  else {
    sequence.durationSmoothing = 'latest'
  }

  if (sequence.shardAffinityRules !== undefined) {
    if (!Array.isArray(sequence.shardAffinityRules)) {
      throw invalid('shardAffinityRules', `must be an array. Received ${JSON.stringify(sequence.shardAffinityRules)}`)
    }
    sequence.shardAffinityRules.forEach((rule, index) => {
      if (rule == null || typeof rule !== 'object' || Array.isArray(rule)) {
        throw invalid('shardAffinityRules', `entry ${index} must be an object with pattern and shardIndex`)
      }
      if (typeof rule.pattern !== 'string') {
        throw invalid('shardAffinityRules', `entry ${index} pattern must be a string. Received ${JSON.stringify(rule.pattern)}`)
      }
      if (typeof rule.shardIndex !== 'number' || !Number.isInteger(rule.shardIndex) || rule.shardIndex < 0) {
        throw invalid('shardAffinityRules', `entry ${index} shardIndex must be an integer >= 0. Received ${JSON.stringify(rule.shardIndex)}`)
      }
    })
  }
  else {
    sequence.shardAffinityRules = []
  }

  if (sequence.rebalanceThreshold !== undefined) {
    sequence.rebalanceThreshold = assertFiniteNumber(
      sequence.rebalanceThreshold,
      'rebalanceThreshold',
      value => (value < 0 || value > 1)
        ? `must be a number between 0 and 1 inclusive. Received ${value}`
        : undefined,
    )
  }
  else {
    sequence.rebalanceThreshold = 0
  }

  if (sequence.isolateSlowThreshold !== undefined) {
    sequence.isolateSlowThreshold = assertFiniteNumber(
      sequence.isolateSlowThreshold,
      'isolateSlowThreshold',
      value => value < 0 ? `must be a finite number >= 0. Received ${value}` : undefined,
    )
  }
  else {
    sequence.isolateSlowThreshold = 0
  }

  if (sequence.durationFallbackStrategy !== undefined) {
    sequence.durationFallbackStrategy = assertEnum(sequence.durationFallbackStrategy, FALLBACKS, 'durationFallbackStrategy')
  }
  else {
    sequence.durationFallbackStrategy = 'hash'
  }

  if (sequence.balanceShardsByTime && !strategyProvided) {
    sequence.shardStrategy = 'time'
  }
  if (sequence.shardStrategy === undefined) {
    sequence.shardStrategy = 'hash'
  }
  if (sequence.shardStrategy !== 'time') {
    sequence.balanceShardsByTime = false
  }
}

export function toDurationHistoryKey(root: string, moduleId: string): string {
  const rootPath = slash(root)
  const fullPath = resolve(rootPath, slash(moduleId))
  let key = slash(relative(rootPath, fullPath))
  if (key.startsWith('./')) {
    key = key.slice(2)
  }
  return key
}

function normalizeHistoryKey(key: string): string {
  let normalized = slash(key).replace(/^\.\/+/, '')
  while (normalized.startsWith('/')) {
    normalized = normalized.slice(1)
  }
  return normalized
}

function historyFilePath(root: string, historyPath: string): string {
  return resolve(slash(root), slash(historyPath))
}

function isObservation(value: unknown): value is DurationObservation {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    return false
  }
  const record = value as Record<string, unknown>
  return typeof record.duration === 'number'
    && Number.isFinite(record.duration)
    && typeof record.recordedAt === 'number'
    && Number.isFinite(record.recordedAt)
}

export function parseHistoryEntry(value: unknown): DurationObservation[] | null {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return [{ duration: value, recordedAt: 0 }]
  }
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    return null
  }
  const record = value as Record<string, unknown>
  if (Array.isArray(record.observations)) {
    const observations: DurationObservation[] = []
    for (const item of record.observations) {
      if (isObservation(item)) {
        observations.push({ duration: item.duration, recordedAt: item.recordedAt })
      }
    }
    return observations
  }
  if (isObservation(record)) {
    return [{ duration: record.duration, recordedAt: record.recordedAt }]
  }
  return null
}

export function applyDurationTtl(
  observations: readonly DurationObservation[],
  ttl: number,
  now = Date.now(),
): DurationObservation[] {
  if (!(ttl > 0)) {
    return observations.slice()
  }
  const cutoff = now - ttl
  return observations.filter(observation => observation.recordedAt === 0 || observation.recordedAt >= cutoff)
}

export function capObservations(
  observations: readonly DurationObservation[],
  maxRuns: number,
): DurationObservation[] {
  const sorted = observations.slice().sort((a, b) => a.recordedAt - b.recordedAt || a.duration - b.duration)
  if (sorted.length <= maxRuns) {
    return sorted
  }
  return sorted.slice(sorted.length - maxRuns)
}

async function readRawHistory(filePath: string): Promise<Record<string, unknown> | null> {
  let text: string
  try {
    text = await fs.promises.readFile(filePath, 'utf8')
  }
  catch {
    return null
  }

  try {
    const parsed = JSON.parse(text) as unknown
    if (parsed == null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    return parsed as Record<string, unknown>
  }
  catch {
    return null
  }
}

export async function readDurationHistory(
  root: string,
  options: DurationHistoryOptions = {},
): Promise<Map<string, DurationObservation[]> | null> {
  const filePath = historyFilePath(root, options.historyPath ?? 'duration-history.json')
  const raw = await readRawHistory(filePath)
  if (raw == null) {
    return null
  }

  const ttl = options.ttl ?? 0
  const now = options.now ?? Date.now()
  const history = new Map<string, DurationObservation[]>()
  for (const [key, value] of Object.entries(raw)) {
    const parsed = parseHistoryEntry(value)
    if (parsed == null) {
      continue
    }
    const fresh = applyDurationTtl(parsed, ttl, now)
    if (fresh.length > 0) {
      history.set(normalizeHistoryKey(key), fresh)
    }
  }
  return history
}

export interface RecordedFileDuration {
  filepath: string
  duration: number | undefined
}

export async function writeFileDurations(options: {
  root: string
  historyPath: string
  maxRuns: number
  files: readonly RecordedFileDuration[]
  now?: number
}): Promise<void> {
  const now = options.now ?? Date.now()
  const filePath = historyFilePath(options.root, options.historyPath)
  const raw = await readRawHistory(filePath) ?? {}

  const updates = new Map<string, number>()
  for (const file of options.files) {
    if (typeof file.duration !== 'number' || !Number.isFinite(file.duration)) {
      continue
    }
    updates.set(toDurationHistoryKey(options.root, file.filepath), Math.round(file.duration))
  }
  if (updates.size === 0) {
    return
  }

  for (const [key, duration] of updates) {
    let existingKey: string | undefined
    let existingValue: unknown
    for (const rawKey of Object.keys(raw)) {
      if (normalizeHistoryKey(rawKey) === key) {
        existingKey = rawKey
        existingValue = raw[rawKey]
        break
      }
    }
    const observations = parseHistoryEntry(existingValue) ?? []
    observations.push({ duration, recordedAt: now })
    const capped = capObservations(observations, options.maxRuns)
    if (existingKey != null && existingKey !== key) {
      delete raw[existingKey]
    }
    raw[key] = options.maxRuns === 1
      ? { duration: capped[0]!.duration, recordedAt: capped[0]!.recordedAt }
      : { observations: capped }
  }

  await fs.promises.mkdir(dirname(filePath), { recursive: true })
  await fs.promises.writeFile(filePath, JSON.stringify(raw))
}

export async function recordFileDurations(ctx: {
  config: {
    root: string
    sequence?: DurationSequenceState
  }
  state: {
    getFiles: () => ReadonlyArray<{ filepath: string; result?: { duration?: number } }>
  }
}): Promise<void> {
  const sequence = ctx.config.sequence
  if (!sequence?.recordFileDurations) {
    return
  }

  await writeFileDurations({
    root: ctx.config.root,
    historyPath: sequence.durationHistoryPath || 'duration-history.json',
    maxRuns: sequence.durationHistoryMaxRuns || 1,
    files: ctx.state.getFiles().map(file => ({
      filepath: file.filepath,
      duration: file.result?.duration,
    })),
  })
}
