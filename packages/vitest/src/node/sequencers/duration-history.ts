import type { File } from '@vitest/runner'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export interface DurationHistoryUpdate {
  key: string
  duration: number
}

export interface DurationHistoryWriteOptions {
  filePath: string
  maxRuns: number
  updates: DurationHistoryUpdate[]
  now: number
}

export interface DurationHistoryContext {
  config: {
    root: string
    sequence?: {
      recordFileDurations?: boolean
      durationHistoryPath?: string
      durationHistoryMaxRuns?: number
    }
  }
  state: {
    getFiles: () => File[]
  }
}

export function toDurationHistoryKey(root: string, filePath: string): string {
  const absolute = resolve(slash(root), slash(filePath))
  return slash(relative(slash(root), absolute))
}

export function resolveDurationHistoryFile(root: string, historyPath: string): string {
  return resolve(slash(root), slash(historyPath))
}

export async function readDurationHistory(
  filePath: string,
  ttl: number = 0,
  now: number = Date.now(),
): Promise<Map<string, DurationObservation[]> | null> {
  let text: string
  try {
    text = await readFile(filePath, 'utf8')
  }
  catch {
    return null
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  }
  catch {
    return null
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return null
  }

  const entries = new Map<string, DurationObservation[]>()
  for (const [key, value] of Object.entries(parsed)) {
    const observations = parseHistoryEntry(value)
    if (observations == null) {
      return null
    }
    const fresh = applyDurationTtl(observations, ttl, now)
    if (fresh.length > 0) {
      entries.set(slash(key), fresh)
    }
  }
  return entries
}

export async function recordFileDurations(ctx: DurationHistoryContext, now: number = Date.now()): Promise<void> {
  const sequence = ctx.config.sequence
  if (!sequence?.recordFileDurations) {
    return
  }

  const updates: DurationHistoryUpdate[] = []
  for (const file of ctx.state.getFiles()) {
    if (file.local) {
      continue
    }
    const duration = file.result?.duration
    if (typeof duration !== 'number' || !Number.isFinite(duration)) {
      continue
    }
    updates.push({
      key: toDurationHistoryKey(ctx.config.root, file.filepath),
      duration: Math.round(duration),
    })
  }

  if (updates.length === 0) {
    return
  }

  const historyPath = sequence.durationHistoryPath ?? 'duration-history.json'
  const maxRuns = sequence.durationHistoryMaxRuns ?? 1
  await writeRecordedDurations({
    filePath: resolveDurationHistoryFile(ctx.config.root, historyPath),
    maxRuns,
    updates,
    now,
  })
}

export async function writeRecordedDurations(options: DurationHistoryWriteOptions): Promise<void> {
  const raw = await readWritableHistory(options.filePath)

  for (const update of options.updates) {
    const existing = parseHistoryEntry(raw[update.key]) ?? []
    existing.push({
      duration: update.duration,
      recordedAt: options.now,
    })
    raw[update.key] = formatStoredEntry(capObservations(existing, options.maxRuns), options.maxRuns)
  }

  await mkdir(dirname(options.filePath), { recursive: true })
  await writeFile(options.filePath, `${JSON.stringify(raw, null, 2)}\n`, 'utf8')
}

export function applyDurationTtl(
  observations: DurationObservation[],
  ttl: number,
  now: number,
): DurationObservation[] {
  if (!(ttl > 0)) {
    return observations
  }
  const cutoff = now - ttl
  return observations.filter(observation =>
    observation.recordedAt === 0 || observation.recordedAt >= cutoff,
  )
}

export function capObservations(
  observations: DurationObservation[],
  maxRuns: number,
): DurationObservation[] {
  const ranked = observations.map((observation, index) => ({ observation, index }))
  ranked.sort((a, b) => {
    if (a.observation.recordedAt !== b.observation.recordedAt) {
      return b.observation.recordedAt - a.observation.recordedAt
    }
    return b.index - a.index
  })
  return ranked
    .slice(0, maxRuns)
    .map(item => item.observation)
    .sort((a, b) => a.recordedAt - b.recordedAt)
}

async function readWritableHistory(filePath: string): Promise<Record<string, unknown>> {
  try {
    const text = await readFile(filePath, 'utf8')
    const parsed = JSON.parse(text) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    const entries = parsed as Record<string, unknown>
    for (const value of Object.values(entries)) {
      if (parseHistoryEntry(value) == null) {
        return {}
      }
    }
    return entries
  }
  catch {
    return {}
  }
}

function formatStoredEntry(
  observations: DurationObservation[],
  maxRuns: number,
): { duration: number; recordedAt: number } | { observations: DurationObservation[] } {
  if (maxRuns === 1) {
    const only = observations[0]
    return {
      duration: only.duration,
      recordedAt: only.recordedAt,
    }
  }
  return { observations }
}

function parseHistoryEntry(value: unknown): DurationObservation[] | null {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return [{ duration: value, recordedAt: 0 }]
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null
  }

  const record = value as Record<string, unknown>
  if ('observations' in record) {
    if (!Array.isArray(record.observations)) {
      return null
    }
    const observations: DurationObservation[] = []
    for (const item of record.observations) {
      if (!isObservation(item)) {
        return null
      }
      observations.push({
        duration: item.duration,
        recordedAt: item.recordedAt,
      })
    }
    return observations
  }

  if (
    typeof record.duration === 'number'
    && Number.isFinite(record.duration)
    && typeof record.recordedAt === 'number'
    && Number.isFinite(record.recordedAt)
  ) {
    return [{
      duration: record.duration,
      recordedAt: record.recordedAt,
    }]
  }

  return null
}

function isObservation(value: unknown): value is DurationObservation {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return false
  }
  const record = value as Record<string, unknown>
  return typeof record.duration === 'number'
    && Number.isFinite(record.duration)
    && typeof record.recordedAt === 'number'
    && Number.isFinite(record.recordedAt)
}
