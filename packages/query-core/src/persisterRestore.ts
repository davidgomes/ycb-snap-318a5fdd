import type { QueryState } from './query'

const persisterRestoreSymbol = Symbol('tanstack-query-persister-restore')

export interface PersisterRestoreResult<TData = unknown, TError = unknown> {
  readonly [persisterRestoreSymbol]: true
  data: TData
  state: Partial<QueryState<TData, TError>>
}

export function createPersisterRestoreResult<TData = unknown, TError = unknown>({
  data,
  state,
}: {
  data: TData
  state: Partial<QueryState<TData, TError>>
}): TData {
  const result: PersisterRestoreResult<TData, TError> = {
    [persisterRestoreSymbol]: true,
    data,
    state,
  }
  // Typed as TData so it can flow through persister / queryFn return types
  return result as unknown as TData
}

export function isPersisterRestoreResult(
  value: unknown,
): value is PersisterRestoreResult<any, any> {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as any)[persisterRestoreSymbol] === true
  )
}
