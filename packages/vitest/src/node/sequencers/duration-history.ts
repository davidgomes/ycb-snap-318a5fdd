import type { Vitest } from '../core'
import type { ResolvedConfig } from '../types/config'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'
import { smoothDuration } from './duration-smoothing'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export type DurationHistory = Record<string, DurationObservation[]>

type DurationHistoryOptions = Pick<
  ResolvedConfig['sequence'],
  'durationHistoryPath' | 'durationHistoryTTL' | 'durationSmoothing' | 'durationHistoryMaxRuns'
>

export function getDurationHistoryKey(root: string, filepath: string): string {
  return slash(relative(slash(root), slash(filepath)))
}

export function resolveDurationHistoryPath(root: string, historyPath?: string): string {
  return resolve(slash(root), historyPath ?? 'duration-history.json')
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isValidDuration(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function parseObservation(value: unknown): DurationObservation | undefined {
  if (!isRecord(value) || !isValidDuration(value.duration)) {
    return undefined
  }
  const recordedAt = typeof value.recordedAt === 'number' && Number.isFinite(value.recordedAt)
    ? value.recordedAt
    : 0
  return { duration: value.duration, recordedAt }
}

export function parseDurationHistory(raw: unknown): DurationHistory | null {
  if (!isRecord(raw)) {
    return null
  }
  const history: DurationHistory = {}
  for (const [file, entry] of Object.entries(raw)) {
    if (isValidDuration(entry)) {
      history[file] = [{ duration: entry, recordedAt: 0 }]
      continue
    }
    if (!isRecord(entry)) {
      continue
    }
    if (Array.isArray(entry.observations)) {
      const observations = entry.observations
        .map(parseObservation)
        .filter((o): o is DurationObservation => o != null)
      if (observations.length) {
        history[file] = observations
      }
      continue
    }
    const observation = parseObservation(entry)
    if (observation) {
      history[file] = [observation]
    }
  }
  return history
}

function readJson(filepath: string): unknown {
  if (!existsSync(filepath)) {
    return undefined
  }
  try {
    return JSON.parse(readFileSync(filepath, 'utf-8'))
  }
  catch {
    return undefined
  }
}

export function readDurationHistory(filepath: string): DurationHistory | null {
  return parseDurationHistory(readJson(filepath))
}

export function filterExpiredObservations(
  observations: DurationObservation[],
  ttl: number,
  now: number = Date.now(),
): DurationObservation[] {
  if (!ttl || ttl <= 0) {
    return observations
  }
  const threshold = now - ttl
  return observations.filter(o => o.recordedAt === 0 || o.recordedAt >= threshold)
}

/**
 * Returns smoothed durations keyed by the path relative to the root,
 * or `null` if the history file is missing or corrupt.
 */
export function loadFileDurations(
  root: string,
  options: Partial<DurationHistoryOptions>,
  now: number = Date.now(),
): Map<string, number> | null {
  const history = readDurationHistory(resolveDurationHistoryPath(root, options.durationHistoryPath))
  if (!history) {
    return null
  }
  const durations = new Map<string, number>()
  for (const [file, observations] of Object.entries(history)) {
    const active = filterExpiredObservations(observations, options.durationHistoryTTL ?? 0, now)
    const duration = smoothDuration(active, options.durationSmoothing ?? 'latest')
    if (duration !== undefined) {
      durations.set(file, duration)
    }
  }
  return durations
}

export function writeDurationHistory(
  filepath: string,
  durations: Map<string, number>,
  maxRuns: number = 1,
  now: number = Date.now(),
): void {
  const raw = readJson(filepath)
  const output: Record<string, unknown> = isRecord(raw) ? { ...raw } : {}
  const existing = parseDurationHistory(raw) ?? {}
  const limit = Math.max(1, maxRuns)

  for (const [file, duration] of durations) {
    const observations = [
      ...(existing[file] ?? []),
      { duration: Math.round(duration), recordedAt: now },
    ]
      .map((observation, index) => ({ observation, index }))
      .sort((a, b) => b.observation.recordedAt - a.observation.recordedAt || b.index - a.index)
      .slice(0, limit)
      .map(({ observation }) => observation)

    output[file] = limit === 1
      ? { duration: observations[0].duration, recordedAt: observations[0].recordedAt }
      : { observations }
  }

  mkdirSync(dirname(filepath), { recursive: true })
  writeFileSync(filepath, `${JSON.stringify(output, null, 2)}\n`, 'utf-8')
}

export function recordFileDurations(ctx: Vitest): void {
  const { root, sequence } = ctx.config
  if (!sequence?.recordFileDurations) {
    return
  }
  const durations = new Map<string, number>()
  for (const file of ctx.state.getFiles()) {
    const duration = file.result?.duration
    if (typeof duration !== 'number' || !Number.isFinite(duration)) {
      continue
    }
    durations.set(getDurationHistoryKey(root, file.filepath), Math.max(0, duration))
  }
  if (!durations.size) {
    return
  }
  writeDurationHistory(
    resolveDurationHistoryPath(root, sequence.durationHistoryPath),
    durations,
    sequence.durationHistoryMaxRuns,
  )
}
