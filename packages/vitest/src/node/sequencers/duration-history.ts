import type { Vitest } from '../core'
import type { DurationObservation, DurationSmoothing } from './duration-smoothing'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { slash } from '@vitest/utils/helpers'
import { dirname, relative, resolve } from 'pathe'
import { smoothDuration } from './duration-smoothing'

export interface DurationHistory {
  files: Map<string, DurationObservation[]>
}

export function applyDurationTtl(observations: DurationObservation[], ttl: number, now: number = Date.now()): DurationObservation[] {
  if (!(ttl > 0)) {
    return observations
  }
  const cutoff = now - ttl
  return observations.filter(observation => observation.recordedAt === 0 || observation.recordedAt >= cutoff)
}

export async function readDurationHistory(root: string, historyPath: string, ttl: number): Promise<DurationHistory | null> {
  const filePath = resolve(root, historyPath)
  let raw: string
  try {
    raw = await readFile(filePath, 'utf-8')
  }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
      return null
    }
    return null
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  }
  catch {
    return null
  }
  if (parsed == null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return null
  }

  const files = new Map<string, DurationObservation[]>()
  for (const [key, value] of Object.entries(parsed)) {
    const observations = parseHistoryEntry(value)
    if (!observations) {
      continue
    }
    const fresh = applyDurationTtl(observations, ttl)
    if (fresh.length > 0) {
      files.set(normalizeHistoryKey(key), fresh)
    }
  }
  return { files }
}

export function durationForFile(history: DurationHistory | null, filePath: string, smoothing: DurationSmoothing): number {
  const observations = history?.files.get(filePath)
  if (!observations || observations.length === 0) {
    return 0
  }
  return smoothDuration(observations, smoothing)
}

export function historyHasFile(history: DurationHistory | null, filePath: string): boolean {
  return !!history?.files.has(filePath)
}

export function normalizeHistoryKey(filePath: string): string {
  const slashed = slash(filePath)
  return slashed.startsWith('/') ? slashed.slice(1) : slashed
}

export async function recordFileDurations(ctx: Vitest): Promise<void> {
  const sequence = ctx.config.sequence
  if (!sequence?.recordFileDurations) {
    return
  }

  const root = ctx.config.root
  const historyPath = sequence.durationHistoryPath || 'duration-history.json'
  const maxRuns = sequence.durationHistoryMaxRuns || 1
  const ttl = sequence.durationHistoryTTL || 0
  const filePath = resolve(root, historyPath)
  const now = Date.now()

  const existing = await readRawHistory(filePath)
  const merged = new Map<string, DurationObservation[]>()
  for (const [key, value] of Object.entries(existing)) {
    const observations = parseHistoryEntry(value)
    if (!observations) {
      continue
    }
    const fresh = applyDurationTtl(observations, ttl, now)
    if (fresh.length > 0) {
      merged.set(normalizeHistoryKey(key), fresh)
    }
  }

  for (const file of ctx.state.getFiles()) {
    const duration = file.result?.duration
    if (typeof duration !== 'number' || !Number.isFinite(duration)) {
      continue
    }
    const key = normalizeHistoryKey(relative(root, file.filepath))
    const observations = merged.get(key) ?? []
    observations.push({
      duration: Math.round(duration),
      recordedAt: now,
    })
    merged.set(key, observations)
  }

  const output: Record<string, unknown> = {}
  for (const [key, observations] of merged) {
    const ranked = observations.map((observation, index) => ({ observation, index }))
    ranked.sort((a, b) => b.observation.recordedAt - a.observation.recordedAt || b.index - a.index)
    const kept = ranked.slice(0, maxRuns).map(entry => entry.observation)
    if (kept.length === 0) {
      continue
    }
    output[key] = maxRuns === 1
      ? kept[0]
      : { observations: kept }
  }

  await mkdir(dirname(filePath), { recursive: true })
  await writeFile(filePath, `${JSON.stringify(output, null, 2)}\n`, 'utf-8')
}

async function readRawHistory(filePath: string): Promise<Record<string, unknown>> {
  try {
    const raw = await readFile(filePath, 'utf-8')
    const parsed = JSON.parse(raw) as unknown
    if (parsed == null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return parsed as Record<string, unknown>
  }
  catch {
    return {}
  }
}

function parseHistoryEntry(value: unknown): DurationObservation[] | null {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return [{ duration: value, recordedAt: 0 }]
  }
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    return null
  }
  const entry = value as { duration?: unknown; recordedAt?: unknown; observations?: unknown }
  if (Array.isArray(entry.observations)) {
    const observations: DurationObservation[] = []
    for (const observation of entry.observations) {
      const parsed = parseObservation(observation)
      if (parsed) {
        observations.push(parsed)
      }
    }
    return observations
  }
  const observation = parseObservation(entry)
  return observation ? [observation] : null
}

function parseObservation(value: unknown): DurationObservation | null {
  if (value == null || typeof value !== 'object') {
    return null
  }
  const entry = value as { duration?: unknown; recordedAt?: unknown }
  if (typeof entry.duration !== 'number' || !Number.isFinite(entry.duration)) {
    return null
  }
  if (typeof entry.recordedAt !== 'number' || !Number.isFinite(entry.recordedAt)) {
    return null
  }
  return { duration: entry.duration, recordedAt: entry.recordedAt }
}
