import type { File } from '@vitest/runner'
import type { Vitest } from '../core'
import type { SequenceDurationSmoothing } from '../types/config'
import { existsSync, promises as fs } from 'node:fs'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'
import { smoothDuration } from './duration-smoothing'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export type DurationHistory = Map<string, DurationObservation[]>

export function getDurationHistoryKey(root: string, filepath: string): string {
  return slash(relative(root, filepath))
}

export function resolveDurationHistoryPath(root: string, historyPath: string): string {
  return resolve(root, historyPath)
}

async function readRawHistory(filepath: string): Promise<Record<string, unknown> | null> {
  if (!existsSync(filepath)) {
    return null
  }
  try {
    const content = JSON.parse(await fs.readFile(filepath, 'utf-8'))
    if (!content || typeof content !== 'object' || Array.isArray(content)) {
      return null
    }
    return content
  }
  catch {
    return null
  }
}

function isValidDuration(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function normalizeObservation(value: unknown): DurationObservation | undefined {
  if (!value || typeof value !== 'object') {
    return undefined
  }
  const { duration, recordedAt } = value as Record<string, unknown>
  if (!isValidDuration(duration)) {
    return undefined
  }
  return {
    duration,
    recordedAt: isValidDuration(recordedAt) ? recordedAt : 0,
  }
}

function normalizeEntry(value: unknown): DurationObservation[] | undefined {
  // legacy format stores the duration as a plain number
  if (isValidDuration(value)) {
    return [{ duration: value, recordedAt: 0 }]
  }
  if (!value || typeof value !== 'object') {
    return undefined
  }
  const observations = (value as Record<string, unknown>).observations
  if (Array.isArray(observations)) {
    return observations
      .map(normalizeObservation)
      .filter(observation => observation != null)
  }
  const observation = normalizeObservation(value)
  return observation ? [observation] : undefined
}

export async function readDurationHistory(
  filepath: string,
  ttl: number,
  now: number = Date.now(),
): Promise<DurationHistory | null> {
  const raw = await readRawHistory(filepath)
  if (!raw) {
    return null
  }
  const history: DurationHistory = new Map()
  for (const key in raw) {
    const observations = normalizeEntry(raw[key])
    if (!observations) {
      continue
    }
    history.set(
      key,
      ttl > 0
        ? observations.filter(o => o.recordedAt === 0 || o.recordedAt >= now - ttl)
        : observations,
    )
  }
  return history
}

export async function readFileDurations(
  filepath: string,
  options: { ttl: number; smoothing: SequenceDurationSmoothing },
): Promise<Map<string, number> | null> {
  const history = await readDurationHistory(filepath, options.ttl)
  if (!history) {
    return null
  }
  const durations = new Map<string, number>()
  history.forEach((observations, key) => {
    const duration = smoothDuration(observations, options.smoothing)
    if (duration !== undefined) {
      durations.set(key, duration)
    }
  })
  return durations
}

export async function writeDurationHistory(
  filepath: string,
  durations: Map<string, number>,
  maxRuns: number,
  now: number = Date.now(),
): Promise<void> {
  const raw = (await readRawHistory(filepath)) ?? {}
  durations.forEach((duration, key) => {
    const observations = [
      ...(normalizeEntry(raw[key]) ?? []),
      { duration, recordedAt: now },
    ]
      .sort((a, b) => a.recordedAt - b.recordedAt)
      .slice(-maxRuns)
    raw[key] = maxRuns === 1 ? observations[0] : { observations }
  })
  await fs.mkdir(dirname(filepath), { recursive: true })
  await fs.writeFile(filepath, `${JSON.stringify(raw, null, 2)}\n`, 'utf-8')
}

export async function recordFileDurations(vitest: Vitest, files: File[]): Promise<void> {
  const { root, sequence } = vitest.config
  if (!sequence.recordFileDurations) {
    return
  }
  const durations = new Map<string, number>()
  for (const file of files) {
    const duration = file.result?.duration
    if (!isValidDuration(duration)) {
      continue
    }
    durations.set(getDurationHistoryKey(root, file.filepath), Math.round(duration))
  }
  if (!durations.size) {
    return
  }
  await writeDurationHistory(
    resolveDurationHistoryPath(root, sequence.durationHistoryPath),
    durations,
    sequence.durationHistoryMaxRuns,
  )
}
