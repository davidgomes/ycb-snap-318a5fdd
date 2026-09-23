import type { DurationObservation, DurationSmoothing } from './duration-smoothing'
import { existsSync, promises as fs, readFileSync } from 'node:fs'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'
import { smoothDurations } from './duration-smoothing'

export type DurationHistory = Record<string, DurationObservation[]>

export interface DurationHistoryOptions {
  root: string
  path: string
  ttl: number
  maxRuns: number
}

export function resolveHistoryPath(root: string, path: string): string {
  return resolve(root, path)
}

export function historyKey(root: string, file: string): string {
  return slash(relative(root, file))
}

function isObservation(value: unknown): value is DurationObservation {
  return typeof value === 'object' && value !== null
    && typeof (value as any).duration === 'number'
    && typeof (value as any).recordedAt === 'number'
}

function parseHistory(raw: unknown): DurationHistory | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    return null
  }
  const history: DurationHistory = {}
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value === 'number') {
      history[key] = [{ duration: value, recordedAt: 0 }]
    }
    else if (isObservation(value)) {
      history[key] = [{ duration: value.duration, recordedAt: value.recordedAt }]
    }
    else if (typeof value === 'object' && value !== null && Array.isArray((value as any).observations)) {
      history[key] = ((value as any).observations as unknown[])
        .filter(isObservation)
        .map(o => ({ duration: o.duration, recordedAt: o.recordedAt }))
    }
  }
  return history
}

function applyTTL(history: DurationHistory, ttl: number): DurationHistory {
  if (!(ttl > 0)) {
    return history
  }
  const cutoff = Date.now() - ttl
  const result: DurationHistory = {}
  for (const [key, observations] of Object.entries(history)) {
    const kept = observations.filter(o => o.recordedAt === 0 || o.recordedAt >= cutoff)
    if (kept.length) {
      result[key] = kept
    }
  }
  return result
}

export function readDurationHistory(options: Pick<DurationHistoryOptions, 'root' | 'path' | 'ttl'>): DurationHistory | null {
  const file = resolveHistoryPath(options.root, options.path)
  if (!existsSync(file)) {
    return null
  }
  try {
    const parsed = parseHistory(JSON.parse(readFileSync(file, 'utf-8')))
    return parsed ? applyTTL(parsed, options.ttl) : null
  }
  catch {
    return null
  }
}

export function getSmoothedDurations(history: DurationHistory, smoothing: DurationSmoothing): Map<string, number> {
  const map = new Map<string, number>()
  for (const [key, observations] of Object.entries(history)) {
    if (observations.length) {
      map.set(key, smoothDurations(observations, smoothing))
    }
  }
  return map
}

export async function writeDurationHistory(
  options: DurationHistoryOptions,
  durations: Map<string, number>,
): Promise<void> {
  const file = resolveHistoryPath(options.root, options.path)
  const existing = readDurationHistory({ ...options, ttl: 0 }) ?? {}
  const now = Date.now()
  for (const [key, duration] of durations) {
    const observations = existing[key] ?? []
    observations.push({ duration: Math.round(duration), recordedAt: now })
    existing[key] = observations
  }
  const maxRuns = Math.max(1, options.maxRuns)
  const output: Record<string, unknown> = {}
  for (const [key, observations] of Object.entries(existing)) {
    const recent = [...observations]
      .sort((a, b) => b.recordedAt - a.recordedAt)
      .slice(0, maxRuns)
    if (!recent.length) {
      continue
    }
    output[key] = maxRuns === 1
      ? { duration: recent[0].duration, recordedAt: recent[0].recordedAt }
      : { observations: recent }
  }
  await fs.mkdir(dirname(file), { recursive: true })
  await fs.writeFile(file, JSON.stringify(output, null, 2), 'utf-8')
}
