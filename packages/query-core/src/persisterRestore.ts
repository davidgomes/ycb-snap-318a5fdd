import type { QueryState } from './query'

/**
 * Marker returned by a query persister when a cached snapshot should be
 * adopted as-is instead of being treated as a successful fetch.
 */
export const persisterRestoreResultSymbol = Symbol('persisterRestoreResult')

export interface PersisterRestoreResult<TData = unknown, TError = unknown> {
  data: TData
  state: QueryState<TData, TError>
  [persisterRestoreResultSymbol]: true
}

export function createPersisterRestoreResult<TData, TError = unknown>(input: {
  data: TData
  state: QueryState<TData, TError>
}): PersisterRestoreResult<TData, TError> {
  return {
    data: input.data,
    state: input.state,
    [persisterRestoreResultSymbol]: true,
  }
}

export function isPersisterRestoreResult<TData = unknown, TError = unknown>(
  value: unknown,
): value is PersisterRestoreResult<TData, TError> {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as PersisterRestoreResult)[persisterRestoreResultSymbol] === true
  )
}
