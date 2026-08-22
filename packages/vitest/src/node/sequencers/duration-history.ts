import type { DurationSmoothing } from '../types/config'
import fs from 'node:fs'
import { mkdir, writeFile } from 'node:fs/promises'
import { slash } from '@vitest/utils/helpers'
import { dirname, resolve } from 'pathe'
import { smoothDuration } from './duration-smoothing'

export interface DurationObservation {
  duration: number
  recordedAt: number
}

export interface DurationHistoryEntry {
  duration?: number
  recordedAt?: number
  observations?: DurationObservation[]
}

export type DurationHistoryRaw = Record<string, DurationHistoryEntry | number>

function normalizePath(path: string): string {
  return slash(path).replace(/^\.\//, '')
}

function getObservations(entry: DurationHistoryEntry | number): DurationObservation[] {
  if (typeof entry === 'number') {
    return [{ duration: entry, recordedAt: 0 }]
  }
  if (entry.observations) {
    return entry.observations
  }
  if (entry.duration != null) {
    return [{ duration: entry.duration, recordedAt: entry.recordedAt ?? 0 }]
  }
  return []
}

function filterByTTL(observations: DurationObservation[], ttl: number): DurationObservation[] {
  if (ttl <= 0) {
    return observations
  }
  const cutoff = Date.now() - ttl
  return observations.filter(o => o.recordedAt === 0 || o.recordedAt >= cutoff)
}

function parseHistoryRaw(raw: string): DurationHistoryRaw | null {
  try {
    const data = JSON.parse(raw)
    if (typeof data !== 'object' || data === null || Array.isArray(data)) {
      return null
    }
    return data as DurationHistoryRaw
  }
  catch {
    return null
  }
}

function readRawHistory(root: string, historyPath: string): DurationHistoryRaw | null {
  const fullPath = resolve(root, historyPath)
  try {
    if (!fs.existsSync(fullPath)) {
      return null
    }
    const raw = fs.readFileSync(fullPath, 'utf8')
    return parseHistoryRaw(raw)
  }
  catch {
    return null
  }
}

export function readDurationHistory(
  root: string,
  historyPath: string,
  ttl: number,
  smoothing: DurationSmoothing,
): Map<string, number> | null {
  const data = readRawHistory(root, historyPath)
  if (data === null) {
    return null
  }

  const result = new Map<string, number>()
  for (const [key, entry] of Object.entries(data)) {
    const observations = filterByTTL(getObservations(entry), ttl)
    if (observations.length === 0) {
      continue
    }
    result.set(normalizePath(key), smoothDuration(observations, smoothing))
  }

  return result
}

function capObservations(observations: DurationObservation[], maxRuns: number): DurationObservation[] {
  return [...observations]
    .sort((a, b) => b.recordedAt - a.recordedAt)
    .slice(0, maxRuns)
}

function formatEntry(observations: DurationObservation[], maxRuns: number): DurationHistoryEntry {
  const capped = capObservations(observations, maxRuns)
  if (maxRuns === 1) {
    const latest = capped[0]
    return { duration: latest.duration, recordedAt: latest.recordedAt }
  }
  return { observations: capped.sort((a, b) => a.recordedAt - b.recordedAt) }
}

export async function writeFileDurations(
  root: string,
  historyPath: string,
  maxRuns: number,
  ttl: number,
  fileDurations: Map<string, number>,
): Promise<void> {
  const existing = readRawHistory(root, historyPath) ?? {}
  const now = Date.now()
  const updated: DurationHistoryRaw = { ...existing }

  for (const [rawPath, duration] of fileDurations) {
    const path = normalizePath(rawPath)
    const entry = updated[path]
    const observations = filterByTTL(getObservations(entry ?? []), ttl)
    observations.push({ duration: Math.round(duration), recordedAt: now })
    updated[path] = formatEntry(observations, maxRuns)
  }

  const fullPath = resolve(root, historyPath)
  await mkdir(dirname(fullPath), { recursive: true })
  await writeFile(fullPath, `${JSON.stringify(updated, null, 2)}\n`, 'utf8')
}
