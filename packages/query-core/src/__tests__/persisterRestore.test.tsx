import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { queryKey } from '@tanstack/query-test-utils'
import {
  InfiniteQueryObserver,
  QueryCache,
  QueryClient,
  QueryObserver,
  createPersisterRestoreResult,
} from '..'
import type { QueryState } from '..'

function restoredState<TData = string, TError extends Error = Error>(
  overrides: Partial<QueryState<TData, TError>> = {},
): QueryState<TData, TError> {
  return {
    data: 'cached' as TData,
    dataUpdateCount: 4,
    dataUpdatedAt: 1_700_000_000_000,
    error: null,
    errorUpdateCount: 2,
    errorUpdatedAt: 1_699_000_000_000,
    fetchFailureCount: 3,
    fetchFailureReason: null,
    fetchMeta: null,
    isInvalidated: true,
    status: 'success',
    fetchStatus: 'idle',
    ...overrides,
  }
}

describe('createPersisterRestoreResult', () => {
  let queryClient: QueryClient
  let queryCache: QueryCache

  beforeEach(() => {
    vi.useFakeTimers()
    queryClient = new QueryClient()
    queryCache = queryClient.getQueryCache()
    queryClient.mount()
  })

  afterEach(() => {
    queryClient.clear()
    vi.useRealTimers()
  })

  test('adopts the persisted snapshot instead of recording a successful fetch', async () => {
    const key = queryKey()
    const onSuccess = vi.fn()
    const onSettled = vi.fn()
    const onError = vi.fn()
    const cache = new QueryCache({ onSuccess, onError, onSettled })
    const client = new QueryClient({ queryCache: cache })
    client.mount()
    const error = new Error('restored failure')
    const state = restoredState({
      data: 'cached',
      error,
      errorUpdatedAt: 1_700_000_100_000,
      errorUpdateCount: 5,
      fetchFailureCount: 5,
      fetchFailureReason: error,
      status: 'error',
      isInvalidated: true,
    })
    const queryFn = vi.fn(() => 'fresh')

    await client.prefetchQuery({
      queryKey: key,
      queryFn,
      persister: () =>
        createPersisterRestoreResult({
          data: 'cached',
          state,
        }),
    })

    const query = cache.find({ queryKey: key })!

    expect(queryFn).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(onSettled).not.toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()
    expect(query.state).toMatchObject({
      data: 'cached',
      dataUpdateCount: 4,
      dataUpdatedAt: state.dataUpdatedAt,
      error,
      errorUpdateCount: 5,
      errorUpdatedAt: state.errorUpdatedAt,
      fetchFailureCount: 5,
      fetchFailureReason: error,
      fetchStatus: 'idle',
      isInvalidated: true,
      status: 'error',
    })

    const observer = new QueryObserver(client, {
      queryKey: key,
      queryFn,
      refetchOnMount: false,
      staleTime: Infinity,
    })
    const unsubscribe = observer.subscribe(() => {})
    const result = observer.getCurrentResult()

    expect(result).toMatchObject({
      status: 'error',
      fetchStatus: 'idle',
      data: 'cached',
      error,
      isError: true,
      isSuccess: false,
      isRefetchError: true,
      isLoadingError: false,
      failureCount: 5,
      failureReason: error,
      errorUpdatedAt: state.errorUpdatedAt,
      dataUpdatedAt: state.dataUpdatedAt,
      errorUpdateCount: 5,
      isStale: true,
    })

    unsubscribe()
    client.clear()
  })

  test('keeps infinite query page params from the restored snapshot', async () => {
    const key = queryKey()
    const data = {
      pages: ['one', 'two'],
      pageParams: [0, 1],
    }
    const state = restoredState<{
      pages: Array<string>
      pageParams: Array<number>
    }>({
      data,
      status: 'success',
      error: null,
      isInvalidated: false,
      fetchFailureCount: 0,
    })

    await queryClient.prefetchInfiniteQuery({
      queryKey: key,
      queryFn: () => 'fresh-page',
      initialPageParam: 0,
      getNextPageParam: (
        _last: string,
        _pages: Array<string>,
        lastParam: number,
      ) => (typeof lastParam === 'number' ? lastParam + 1 : undefined),
      persister: () => createPersisterRestoreResult({ data, state }),
    })

    const query = queryCache.find({ queryKey: key })!
    expect(query.state.data).toEqual(data)
    expect(query.state.fetchStatus).toBe('idle')
    expect(query.state.dataUpdatedAt).toBe(state.dataUpdatedAt)
    expect(query.state.dataUpdateCount).toBe(state.dataUpdateCount)

    const observer = new InfiniteQueryObserver(queryClient, {
      queryKey: key,
      queryFn: () => 'fresh-page',
      initialPageParam: 0,
      getNextPageParam: (
        _last: string,
        _pages: Array<string>,
        lastParam: number,
      ) => (typeof lastParam === 'number' ? lastParam + 1 : undefined),
      refetchOnMount: false,
      staleTime: Infinity,
    })
    const result = observer.getCurrentResult()

    expect(result.data).toEqual(data)
    expect(result.data?.pageParams).toEqual([0, 1])
    expect(result.hasNextPage).toBe(true)
    expect(result.fetchStatus).toBe('idle')
    expect(result.failureCount).toBe(0)
    expect(result.dataUpdatedAt).toBe(state.dataUpdatedAt)
  })

  test('does not treat a plain data payload as a restored snapshot', async () => {
    const key = queryKey()
    const payload = {
      data: 'cached',
      state: restoredState({ status: 'error' }),
    }

    await queryClient.prefetchQuery({
      queryKey: key,
      queryFn: () => payload,
      persister: () => payload,
    })

    const query = queryCache.find({ queryKey: key })!
    expect(query.state.data).toEqual(payload)
    expect(query.state.status).toBe('success')
    expect(query.state.error).toBeNull()
  })

  test('preserves an error snapshot that has no data', async () => {
    const key = queryKey()
    const error = new Error('empty')
    const state = restoredState({
      data: undefined,
      status: 'error',
      error,
      dataUpdateCount: 0,
      dataUpdatedAt: 0,
      errorUpdateCount: 2,
      errorUpdatedAt: 50,
      fetchFailureCount: 2,
      fetchFailureReason: error,
      isInvalidated: true,
    })

    await queryClient.prefetchQuery({
      queryKey: key,
      queryFn: () => 'fresh',
      persister: () =>
        createPersisterRestoreResult({
          data: undefined as unknown as string,
          state,
        }),
    })

    const query = queryCache.find({ queryKey: key })!
    expect(query.state.status).toBe('error')
    expect(query.state.data).toBeUndefined()
    expect(query.state.error).toBe(error)
    expect(query.state.fetchStatus).toBe('idle')
    expect(query.state.fetchFailureCount).toBe(2)
    expect(query.state.errorUpdatedAt).toBe(50)
  })
})
