import type { DurationSmoothing } from '../types/config'
import type { DurationObservation } from './duration-smoothing'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'
import { smoothDuration } from './duration-smoothing'

export type { DurationObservation } from './duration-smoothing'

export interface DurationHistoryReadOptions {
  ttl?: number
  now?: number
}

export interface FileDuration {
  duration: number
  known: boolean
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value)
}

function normalizeHistoryKey(key: string): string {
  let normalized = slash(key)
  while (normalized.startsWith('./')) {
    normalized = normalized.slice(2)
  }
  while (normalized.startsWith('/')) {
    normalized = normalized.slice(1)
  }
  return normalized
}

function parseObservation(value: unknown): DurationObservation | null {
  if (!isPlainObject(value)) {
    return null
  }
  if (typeof value.duration !== 'number' || !Number.isFinite(value.duration)) {
    return null
  }
  if (typeof value.recordedAt !== 'number' || !Number.isFinite(value.recordedAt)) {
    return null
  }
  return {
    duration: value.duration,
    recordedAt: value.recordedAt,
  }
}

export function filterDurationObservations(
  observations: readonly DurationObservation[],
  ttl: number,
  now: number,
): DurationObservation[] {
  if (!(ttl > 0)) {
    return observations.slice()
  }
  const cutoff = now - ttl
  return observations.filter(observation =>
    observation.recordedAt === 0 || observation.recordedAt >= cutoff,
  )
}

export function parseDurationHistory(
  raw: string,
  options: DurationHistoryReadOptions = {},
): Map<string, DurationObservation[]> | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  }
  catch {
    return null
  }
  if (!isPlainObject(parsed)) {
    return null
  }

  const ttl = options.ttl ?? 0
  const now = options.now ?? Date.now()
  const history = new Map<string, DurationObservation[]>()

  for (const [key, value] of Object.entries(parsed)) {
    let observations: DurationObservation[] | null = null
    if (typeof value === 'number') {
      if (!Number.isFinite(value)) {
        return null
      }
      observations = [{ duration: value, recordedAt: 0 }]
    }
    else if (isPlainObject(value) && 'observations' in value) {
      if (!Array.isArray(value.observations)) {
        return null
      }
      observations = []
      for (const item of value.observations) {
        const observation = parseObservation(item)
        if (!observation) {
          return null
        }
        observations.push(observation)
      }
    }
    else if (isPlainObject(value) && ('duration' in value || 'recordedAt' in value)) {
      const observation = parseObservation(value)
      if (!observation) {
        return null
      }
      observations = [observation]
    }
    else {
      return null
    }

    const fresh = filterDurationObservations(observations, ttl, now)
    if (fresh.length > 0) {
      history.set(normalizeHistoryKey(key), fresh)
    }
  }

  return history
}

export async function readDurationHistory(
  filePath: string,
  options: DurationHistoryReadOptions = {},
): Promise<Map<string, DurationObservation[]> | null> {
  let raw: string
  try {
    raw = await readFile(filePath, 'utf-8')
  }
  catch {
    return null
  }
  return parseDurationHistory(raw, options)
}

export function historyFilePath(root: string, historyPath?: string): string {
  return resolve(root, historyPath || 'duration-history.json')
}

export async function readProjectDurationHistory(
  root: string | undefined,
  options: { durationHistoryPath?: string; durationHistoryTTL?: number; now?: number } = {},
): Promise<Map<string, DurationObservation[]> | null> {
  if (!root) {
    return null
  }
  return readDurationHistory(historyFilePath(root, options.durationHistoryPath), {
    ttl: options.durationHistoryTTL ?? 0,
    now: options.now,
  })
}

export function relativeModulePath(root: string, moduleId: string): string {
  const fullPath = resolve(slash(root), slash(moduleId))
  let modulePath = slash(relative(slash(root), fullPath))
  if (modulePath.startsWith('./')) {
    modulePath = modulePath.slice(2)
  }
  return modulePath
}

export function resolveFileDurations<T>(
  files: readonly T[],
  history: Map<string, DurationObservation[]> | null,
  smoothing: DurationSmoothing,
  getPath: (file: T) => string,
): Map<T, FileDuration> {
  const resolved = new Map<T, FileDuration>()
  for (const file of files) {
    const observations = history?.get(getPath(file))
    if (!history || !observations || observations.length === 0) {
      resolved.set(file, { duration: 0, known: false })
      continue
    }
    resolved.set(file, {
      duration: smoothDuration(observations, smoothing),
      known: true,
    })
  }
  return resolved
}

export function capDurationObservations(
  observations: readonly DurationObservation[],
  maxRuns: number,
): DurationObservation[] {
  const indexed = observations.map((observation, index) => ({ observation, index }))
  indexed.sort((left, right) =>
    left.observation.recordedAt - right.observation.recordedAt || left.index - right.index,
  )
  return indexed.slice(Math.max(0, indexed.length - maxRuns)).map(item => item.observation)
}

export async function writeDurationHistory(
  filePath: string,
  files: ReadonlyMap<string, readonly DurationObservation[]>,
  maxRuns: number,
): Promise<void> {
  const output: Record<string, unknown> = {}
  const keys = [...files.keys()].sort()
  for (const key of keys) {
    const capped = capDurationObservations(files.get(key) ?? [], maxRuns)
    if (capped.length === 0) {
      continue
    }
    if (maxRuns === 1) {
      output[key] = {
        duration: capped[0].duration,
        recordedAt: capped[0].recordedAt,
      }
    }
    else {
      output[key] = {
        observations: capped.map(observation => ({
          duration: observation.duration,
          recordedAt: observation.recordedAt,
        })),
      }
    }
  }

  await mkdir(dirname(filePath), { recursive: true })
  await writeFile(filePath, `${JSON.stringify(output, null, 2)}\n`)
}

export async function recordFileDurations(ctx: {
  config: {
    root: string
    sequence?: {
      recordFileDurations?: boolean
      durationHistoryPath?: string
      durationHistoryTTL?: number
      durationHistoryMaxRuns?: number
    }
  }
  state: {
    getFiles: () => Array<{
      filepath: string
      result?: { duration?: number }
    }>
  }
}): Promise<void> {
  const sequence = ctx.config.sequence
  if (!sequence?.recordFileDurations) {
    return
  }

  const now = Date.now()
  const maxRuns = sequence.durationHistoryMaxRuns ?? 1
  const ttl = sequence.durationHistoryTTL ?? 0
  const filePath = historyFilePath(ctx.config.root, sequence.durationHistoryPath)
  const existing = await readDurationHistory(filePath, { ttl, now })
  const files = new Map<string, DurationObservation[]>()

  if (existing) {
    for (const [key, observations] of existing) {
      files.set(key, observations.slice())
    }
  }

  for (const file of ctx.state.getFiles()) {
    const duration = file.result?.duration
    if (typeof duration !== 'number' || !Number.isFinite(duration)) {
      continue
    }
    const key = relativeModulePath(ctx.config.root, file.filepath)
    const observations = files.get(key)?.slice() ?? []
    observations.push({
      duration: Math.round(duration),
      recordedAt: now,
    })
    files.set(key, observations)
  }

  await writeDurationHistory(filePath, files, maxRuns)
}
