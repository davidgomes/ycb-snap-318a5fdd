import type { QueryState } from './query'
import type { DefaultError, PersisterRestoreResult, QueryStatus } from './types'

const persisterRestoreResults = new WeakSet<object>()

/**
 * Mark a persisted query snapshot so `prefetchQuery` and query observers
 * adopt `state` instead of storing `data` as a new successful fetch.
 */
export function createPersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
>(persisted: {
  data: TData
  state: Partial<QueryState<TData, TError>>
}): PersisterRestoreResult<TData, TError> {
  const result: PersisterRestoreResult<TData, TError> = {
    data: persisted.data,
    state: persisted.state,
  }
  persisterRestoreResults.add(result)
  return result
}

export function isPersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
>(value: unknown): value is PersisterRestoreResult<TData, TError> {
  return (
    typeof value === 'object' &&
    value !== null &&
    persisterRestoreResults.has(value)
  )
}

export function isInitialQueryState<TData, TError>(
  state: QueryState<TData, TError>,
): boolean {
  return (
    state.data === undefined &&
    state.error == null &&
    state.status === 'pending' &&
    state.dataUpdatedAt === 0 &&
    state.errorUpdatedAt === 0 &&
    state.dataUpdateCount === 0 &&
    state.errorUpdateCount === 0 &&
    state.fetchFailureCount === 0 &&
    !state.isInvalidated
  )
}

export function normalizeRestoredQueryState<TData, TError = DefaultError>(
  state: Partial<QueryState<TData, TError>> | undefined,
): QueryState<TData, TError> {
  const data = state?.data
  const error = state?.error ?? null
  const hasData = data !== undefined
  const derivedStatus: QueryStatus =
    error != null ? 'error' : hasData ? 'success' : 'pending'
  // An explicit error status must survive even when the error value itself
  // was not serialized. A present error is always a failed snapshot.
  const status: QueryStatus =
    error != null ? 'error' : (state?.status ?? derivedStatus)
  const hasError = error != null || status === 'error'

  return {
    data,
    dataUpdateCount: state?.dataUpdateCount ?? (hasData ? 1 : 0),
    dataUpdatedAt: state?.dataUpdatedAt ?? 0,
    error,
    errorUpdateCount: state?.errorUpdateCount ?? (hasError ? 1 : 0),
    errorUpdatedAt: state?.errorUpdatedAt ?? 0,
    fetchFailureCount: state?.fetchFailureCount ?? 0,
    fetchFailureReason: (state?.fetchFailureReason ?? null) as TError | null,
    fetchMeta: state?.fetchMeta ?? null,
    isInvalidated: state?.isInvalidated ?? false,
    status,
    fetchStatus: 'idle',
  }
}

/**
 * Pick data and error independently by their own timestamps.
 * A newer successful payload must not erase a newer refetch error, and a
 * newer error must not discard newer cached data.
 */
export function mergeRestoredQueryState<TData, TError>(
  current: QueryState<TData, TError>,
  restored: QueryState<TData, TError>,
): QueryState<TData, TError> {
  const useRestoredData =
    restored.dataUpdatedAt > current.dataUpdatedAt ||
    (restored.dataUpdatedAt === current.dataUpdatedAt &&
      current.data === undefined &&
      restored.data !== undefined)

  const useRestoredError =
    restored.errorUpdatedAt > current.errorUpdatedAt ||
    (restored.errorUpdatedAt === current.errorUpdatedAt &&
      current.error == null &&
      current.status !== 'error' &&
      (restored.error != null || restored.status === 'error'))

  const dataState = useRestoredData ? restored : current
  const errorState = useRestoredError ? restored : current
  const data = dataState.data
  const error = errorState.error
  const status: QueryStatus =
    error != null || errorState.status === 'error'
      ? 'error'
      : data !== undefined
        ? 'success'
        : dataState.status === 'error'
          ? 'pending'
          : dataState.status

  return {
    data,
    dataUpdateCount: dataState.dataUpdateCount,
    dataUpdatedAt: dataState.dataUpdatedAt,
    error,
    errorUpdateCount: errorState.errorUpdateCount,
    errorUpdatedAt: errorState.errorUpdatedAt,
    fetchFailureCount: errorState.fetchFailureCount,
    fetchFailureReason: errorState.fetchFailureReason,
    fetchMeta: dataState.fetchMeta,
    isInvalidated:
      status === 'error' ? errorState.isInvalidated : dataState.isInvalidated,
    status,
    fetchStatus: 'idle',
  }
}
