import type { Vitest } from '../core'
import type { TestSpecification } from '../test-specification'
import type { ResolvedConfig } from '../types/config'
import type { DurationObservation } from './duration-smoothing'
import { promises as fs } from 'node:fs'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'
import { smoothDuration } from './duration-smoothing'

export type DurationHistoryEntry
  = | number
    | DurationObservation
    | { observations: DurationObservation[] }

/**
 * Non-expired observations keyed by the test file path relative to the root.
 */
export type DurationHistory = Map<string, DurationObservation[]>

export interface DurationHistoryConfig {
  root: string
  sequence: Partial<Pick<
    ResolvedConfig['sequence'],
    'durationHistoryPath' | 'durationHistoryTTL' | 'durationHistoryMaxRuns' | 'durationSmoothing'
  >>
}

export function getDurationHistoryPath(config: DurationHistoryConfig): string {
  return resolve(config.root, config.sequence.durationHistoryPath ?? 'duration-history.json')
}

export function getDurationHistoryKey(root: string, moduleId: string): string {
  const normalizedRoot = slash(root)
  return relative(normalizedRoot, resolve(normalizedRoot, slash(moduleId)))
}

export function parseDurationHistoryEntry(entry: unknown): DurationObservation[] {
  if (isDuration(entry)) {
    return [{ duration: entry, recordedAt: 0 }]
  }
  if (!isRecord(entry)) {
    return []
  }
  if (Array.isArray(entry.observations)) {
    return entry.observations.map(parseObservation).filter(observation => observation != null)
  }
  const observation = parseObservation(entry)
  return observation ? [observation] : []
}

export async function readDurationHistory(
  config: DurationHistoryConfig,
  now: number = Date.now(),
): Promise<DurationHistory | null> {
  const file = await readDurationHistoryFile(getDurationHistoryPath(config))
  if (!file) {
    return null
  }

  const ttl = config.sequence.durationHistoryTTL ?? 0
  const history: DurationHistory = new Map()
  for (const key in file) {
    let observations = parseDurationHistoryEntry(file[key])
    if (ttl > 0) {
      const expiresBefore = now - ttl
      observations = observations.filter(({ recordedAt }) => recordedAt === 0 || recordedAt >= expiresBefore)
    }
    if (observations.length) {
      history.set(slash(key), observations)
    }
  }
  return history
}

/**
 * Returns smoothed durations keyed by the test file path relative to the root,
 * or `null` if the history file is missing or corrupt.
 */
export async function loadFileDurations(config: DurationHistoryConfig): Promise<Map<string, number> | null> {
  const history = await readDurationHistory(config)
  if (!history) {
    return null
  }

  const smoothing = config.sequence.durationSmoothing ?? 'latest'
  const durations = new Map<string, number>()
  history.forEach((observations, key) => {
    durations.set(key, smoothDuration(observations, smoothing)!)
  })
  return durations
}

export async function recordFileDurations(ctx: Vitest, specifications: TestSpecification[]): Promise<void> {
  const { config } = ctx
  const moduleIds = new Set(specifications.map(spec => spec.moduleId))
  const durations = new Map<string, number>()

  for (const file of ctx.state.getFiles()) {
    const duration = file.result?.duration
    if (!moduleIds.has(file.filepath) || !isDuration(duration)) {
      continue
    }
    const key = getDurationHistoryKey(config.root, file.filepath)
    durations.set(key, Math.max(durations.get(key) ?? 0, Math.round(duration)))
  }

  if (!durations.size) {
    return
  }

  const path = getDurationHistoryPath(config)
  const file = (await readDurationHistoryFile(path)) ?? {}
  const maxRuns = config.sequence.durationHistoryMaxRuns ?? 1
  const recordedAt = Date.now()

  durations.forEach((duration, key) => {
    const observations = [...parseDurationHistoryEntry(file[key]), { duration, recordedAt }]
      .sort((a, b) => a.recordedAt - b.recordedAt)
      .slice(-maxRuns)
    file[key] = maxRuns === 1 ? observations[0] : { observations }
  })

  await fs.mkdir(dirname(path), { recursive: true })
  await fs.writeFile(path, `${JSON.stringify(file, null, 2)}\n`, 'utf-8')
}

async function readDurationHistoryFile(path: string): Promise<Record<string, unknown> | null> {
  try {
    const content: unknown = JSON.parse(await fs.readFile(path, 'utf-8'))
    return isRecord(content) ? content : null
  }
  catch {
    return null
  }
}

function parseObservation(value: unknown): DurationObservation | null {
  if (!isRecord(value) || !isDuration(value.duration)) {
    return null
  }
  const recordedAt = typeof value.recordedAt === 'number' && Number.isFinite(value.recordedAt)
    ? value.recordedAt
    : 0
  return { duration: value.duration, recordedAt }
}

function isDuration(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
