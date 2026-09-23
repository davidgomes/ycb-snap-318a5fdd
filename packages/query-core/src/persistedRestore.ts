import type { DefaultError, QueryStatus } from './types'
import type { QueryState } from './query'

export const persisterRestoreResultSymbol: unique symbol = Symbol(
  'persisterRestoreResult',
)

/**
 * Marker returned from a `persister` to adopt a cached snapshot instead of
 * recording a fresh successful fetch.
 */
export interface PersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
> {
  data: TData
  state: Partial<QueryState<TData, TError>>
  [persisterRestoreResultSymbol]: true
}

/**
 * Build the value a persister returns from `prefetchQuery` or a query
 * observer so TanStack Query restores `state` instead of treating the
 * resolution as a new success fetch.
 */
export function createPersisterRestoreResult<
  TData,
  TError = DefaultError,
>(restored: {
  data: TData
  state: Partial<QueryState<TData, TError>>
}): PersisterRestoreResult<TData, TError> {
  return {
    data: restored.data,
    state: restored.state,
    [persisterRestoreResultSymbol]: true,
  }
}

export function isPersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
>(value: unknown): value is PersisterRestoreResult<TData, TError> {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as { [persisterRestoreResultSymbol]?: unknown })[
      persisterRestoreResultSymbol
    ] === true
  )
}

/**
 * Turn a persisted snapshot into the query state that should be active after
 * restoration. `fetchStatus` is always idle. Status stays an error when an
 * error is present so observers can surface `isRefetchError`.
 */
export function toRestoredQueryState<TData, TError = DefaultError>(
  state: Partial<QueryState<TData, TError>> | undefined,
  data: TData | undefined,
): QueryState<TData, TError> {
  const error = (state?.error ?? null) as TError | null
  const status: QueryStatus =
    error != null ? 'error' : data !== undefined ? 'success' : 'pending'

  return {
    data,
    dataUpdateCount:
      state?.dataUpdateCount !== undefined
        ? state.dataUpdateCount
        : data !== undefined
          ? 1
          : 0,
    dataUpdatedAt: state?.dataUpdatedAt ?? 0,
    error,
    errorUpdateCount:
      state?.errorUpdateCount !== undefined
        ? state.errorUpdateCount
        : error != null
          ? 1
          : 0,
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
 * Merge a persisted snapshot into a live query by comparing data freshness
 * and error freshness separately. Newer data is never discarded because the
 * other side has a newer error, and a newer error is still applied on top of
 * newer data so the result stays a refetch error.
 */
export function reconcileRestoredQueryState<TData, TError = DefaultError>(
  current: QueryState<TData, TError> | undefined,
  restored: {
    data: TData | undefined
    state: Partial<QueryState<TData, TError>>
  },
): QueryState<TData, TError> {
  const next = toRestoredQueryState(restored.state, restored.data)
  if (!current) {
    return next
  }

  const restoredDataIsNewer = next.dataUpdatedAt > current.dataUpdatedAt
  const restoredErrorIsNewer = next.errorUpdatedAt > current.errorUpdatedAt
  const dataState = restoredDataIsNewer ? next : current
  const errorState = restoredErrorIsNewer ? next : current
  const data = dataState.data
  const error = errorState.error
  const status: QueryStatus =
    error != null ? 'error' : data !== undefined ? 'success' : 'pending'

  return {
    data,
    dataUpdateCount: dataState.dataUpdateCount,
    dataUpdatedAt: dataState.dataUpdatedAt,
    error,
    errorUpdateCount: errorState.errorUpdateCount,
    errorUpdatedAt: errorState.errorUpdatedAt,
    fetchFailureCount: errorState.fetchFailureCount,
    fetchFailureReason: errorState.fetchFailureReason,
    fetchMeta: dataState.fetchMeta ?? null,
    isInvalidated:
      dataState.isInvalidated || (error != null && errorState.isInvalidated),
    status,
    fetchStatus: 'idle',
  }
}
