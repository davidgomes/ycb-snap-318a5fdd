import type { QueryState } from './query'
import type { DefaultError, QueryStatus } from './types'

const PERSISTER_RESTORE_RESULT = Symbol.for(
  '@tanstack/query/persisterRestoreResult',
)

export interface PersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
> {
  data: TData
  state: Partial<QueryState<TData, TError>>
}

/**
 * Marks a persister return value as a restored cache snapshot.
 * `prefetchQuery` and query observers adopt `state` instead of storing the
 * value as a successful fetch.
 */
export function createPersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
>(input: {
  data: TData
  state: Partial<QueryState<TData, TError>>
}): PersisterRestoreResult<TData, TError> {
  const result: PersisterRestoreResult<TData, TError> = {
    data: input.data,
    state: input.state,
  }

  Object.defineProperty(result, PERSISTER_RESTORE_RESULT, {
    value: true,
  })

  return result
}

export function isPersisterRestoreResult<
  TData = unknown,
  TError = DefaultError,
>(value: unknown): value is PersisterRestoreResult<TData, TError> {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as { [PERSISTER_RESTORE_RESULT]?: unknown })[
      PERSISTER_RESTORE_RESULT
    ] === true
  )
}

export function normalizeRestoredQueryState<
  TData = unknown,
  TError = DefaultError,
>(
  data: TData,
  state?: Partial<QueryState<TData, TError>>,
): QueryState<TData, TError> {
  const resolvedData = data !== undefined ? data : (state?.data as TData)
  const error = (state?.error ?? null) as TError | null
  const hasData = resolvedData !== undefined
  const hasError = error != null
  const status: QueryStatus = hasError
    ? 'error'
    : (state?.status ?? (hasData ? 'success' : 'pending'))

  return {
    data: resolvedData,
    dataUpdateCount: state?.dataUpdateCount ?? (hasData ? 1 : 0),
    dataUpdatedAt: state?.dataUpdatedAt ?? 0,
    error,
    errorUpdateCount: state?.errorUpdateCount ?? (hasError ? 1 : 0),
    errorUpdatedAt: state?.errorUpdatedAt ?? 0,
    fetchFailureCount: state?.fetchFailureCount ?? 0,
    fetchFailureReason: state?.fetchFailureReason ?? null,
    fetchMeta: state?.fetchMeta ?? null,
    isInvalidated: state?.isInvalidated ?? false,
    status,
    fetchStatus: 'idle',
  }
}

/**
 * Keep the newer cached value and the newer error metadata as separate
 * decisions. A newer value must not drop a newer refetch error, and a newer
 * error must not drop a newer value.
 */
export function mergeRestoredQueryState<TData = unknown, TError = DefaultError>(
  current: QueryState<TData, TError>,
  persisted: QueryState<TData, TError>,
): QueryState<TData, TError> {
  const usePersistedData = persisted.dataUpdatedAt > current.dataUpdatedAt
  const usePersistedError = persisted.errorUpdatedAt > current.errorUpdatedAt

  if (!usePersistedData && !usePersistedError) {
    return current
  }

  const dataSide = usePersistedData ? persisted : current
  const errorSide = usePersistedError ? persisted : current
  const data = dataSide.data
  const error = errorSide.error
  const hasError = error != null || errorSide.status === 'error'
  const status: QueryStatus = hasError
    ? 'error'
    : data !== undefined
      ? 'success'
      : 'pending'

  return {
    data,
    dataUpdateCount: dataSide.dataUpdateCount,
    dataUpdatedAt: dataSide.dataUpdatedAt,
    error,
    errorUpdateCount: errorSide.errorUpdateCount,
    errorUpdatedAt: errorSide.errorUpdatedAt,
    fetchFailureCount: errorSide.fetchFailureCount,
    fetchFailureReason: errorSide.fetchFailureReason,
    fetchMeta: null,
    isInvalidated: dataSide.isInvalidated,
    status,
    fetchStatus: 'idle',
  }
}
