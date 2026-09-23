import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { QueryCache, QueryClient, QueryObserver } from '..'
import {
  createPersisterRestoreResult,
  isPersisterRestoreResult,
  mergeRestoredQueryState,
  normalizeRestoredQueryState,
} from '../persisterRestore'
import type { QueryObserverResult, QueryState } from '..'

function snapshotState(
  overrides: Partial<QueryState<string, Error>> = {},
): QueryState<string, Error> {
  return {
    data: undefined,
    dataUpdateCount: 0,
    dataUpdatedAt: 0,
    error: null,
    errorUpdateCount: 0,
    errorUpdatedAt: 0,
    fetchFailureCount: 0,
    fetchFailureReason: null,
    fetchMeta: null,
    isInvalidated: false,
    status: 'pending',
    fetchStatus: 'idle',
    ...overrides,
  }
}

describe('createPersisterRestoreResult', () => {
  let queryClient: QueryClient
  let queryCache: QueryCache

  beforeEach(() => {
    vi.useFakeTimers()
    queryCache = new QueryCache()
    queryClient = new QueryClient({ queryCache })
    queryClient.mount()
  })

  afterEach(() => {
    queryClient.clear()
    vi.useRealTimers()
  })

  test('brands a snapshot without treating a plain object as one', () => {
    const error = new Error('refetch failed')
    const restored = createPersisterRestoreResult({
      data: 'cached',
      state: {
        dataUpdatedAt: 100,
        error,
        status: 'error',
      },
    })

    expect(isPersisterRestoreResult(restored)).toBe(true)
    expect(
      isPersisterRestoreResult({
        data: 'cached',
        state: { status: 'error' },
      }),
    ).toBe(false)
    expect(Object.keys(restored).sort()).toEqual(['data', 'state'])
  })

  test('adopts the snapshot instead of recording a successful fetch', async () => {
    const key = ['restored']
    const error = new Error('refetch failed')
    const onSuccess = vi.fn()
    const onError = vi.fn()
    const onSettled = vi.fn()
    const client = new QueryClient({
      queryCache: new QueryCache({ onSuccess, onError, onSettled }),
    })
    client.mount()
    const queryFn = vi.fn(() => 'fresh')
    const pages = {
      pages: ['one', 'two'],
      pageParams: [0, 1],
    }

    const data = await client.fetchQuery({
      queryKey: key,
      queryFn,
      persister: () =>
        createPersisterRestoreResult({
          data: 'cached',
          state: {
            dataUpdatedAt: 100,
            dataUpdateCount: 4,
            error,
            errorUpdatedAt: 250,
            errorUpdateCount: 3,
            fetchFailureCount: 4,
            fetchFailureReason: error,
            isInvalidated: true,
            status: 'success',
            fetchStatus: 'fetching',
          },
        }),
    })

    expect(data).toBe('cached')
    expect(queryFn).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()
    expect(onSettled).not.toHaveBeenCalled()

    const query = client.getQueryCache().find({ queryKey: key })!
    expect(query.state).toMatchObject({
      data: 'cached',
      dataUpdatedAt: 100,
      dataUpdateCount: 4,
      error,
      errorUpdatedAt: 250,
      errorUpdateCount: 3,
      fetchFailureCount: 4,
      fetchFailureReason: error,
      isInvalidated: true,
      status: 'error',
      fetchStatus: 'idle',
    })

    await client.prefetchInfiniteQuery({
      queryKey: ['infinite'],
      queryFn: () => 'page',
      initialPageParam: 0,
      getNextPageParam: () => 1,
      persister: () =>
        createPersisterRestoreResult({
          data: pages,
          state: {
            dataUpdatedAt: 100,
            dataUpdateCount: 2,
            status: 'success',
          },
        }),
    })

    expect(client.getQueryData(['infinite'])).toEqual(pages)
    client.clear()
  })

  test('keeps a plain data object that happens to contain data and state', async () => {
    const key = ['plain']
    const payload = {
      data: 'payload',
      state: { status: 'error' as const, fetchFailureCount: 9 },
    }
    const onSuccess = vi.fn()
    const client = new QueryClient({
      queryCache: new QueryCache({ onSuccess }),
    })
    client.mount()

    const data = await client.fetchQuery({
      queryKey: key,
      queryFn: () => payload,
      persister: (queryFn, context) => queryFn(context),
    })

    expect(data).toBe(payload)
    expect(onSuccess).toHaveBeenCalledTimes(1)
    expect(client.getQueryState(key)).toMatchObject({
      data: payload,
      status: 'success',
      error: null,
      fetchFailureCount: 0,
    })
    client.clear()
  })

  test('observer results expose the restored failure metadata', async () => {
    const key = ['observer']
    const error = new Error('refetch failed')
    const queryFn = vi.fn(() => 'fresh')

    const observer = new QueryObserver(queryClient, {
      queryKey: key,
      queryFn,
      staleTime: 'static',
      _optimisticResults: 'optimistic',
      persister: () =>
        createPersisterRestoreResult({
          data: 'cached',
          state: {
            dataUpdatedAt: 100,
            dataUpdateCount: 4,
            error,
            errorUpdatedAt: 250,
            errorUpdateCount: 3,
            fetchFailureCount: 4,
            fetchFailureReason: error,
            isInvalidated: true,
            status: 'error',
          },
        }),
    })

    const result = await new Promise<QueryObserverResult<string, Error>>(
      (resolve) => {
        const unsubscribe = observer.subscribe((value) => {
          if (value.fetchStatus === 'idle' && value.data === 'cached') {
            unsubscribe()
            resolve(value)
          }
        })
      },
    )

    expect(queryFn).not.toHaveBeenCalled()
    expect(result).toMatchObject({
      data: 'cached',
      dataUpdatedAt: 100,
      error,
      errorUpdatedAt: 250,
      errorUpdateCount: 3,
      failureCount: 4,
      failureReason: error,
      fetchStatus: 'idle',
      status: 'error',
      isError: true,
      isSuccess: false,
      isRefetchError: true,
      isLoadingError: false,
      isFetching: false,
    })
    expect(queryClient.getQueryState(key)?.isInvalidated).toBe(true)

    const mounted = new QueryObserver(queryClient, {
      queryKey: key,
      queryFn,
      staleTime: 0,
      _optimisticResults: 'optimistic',
    }).getCurrentResult()

    expect(mounted.failureCount).toBe(4)
    expect(mounted.dataUpdatedAt).toBe(100)
    expect(mounted.errorUpdatedAt).toBe(250)
    expect(mounted.isRefetchError).toBe(true)
    expect(mounted.data).toBe('cached')
  })

  test('normalizes partial snapshots and merges freshness independently', () => {
    const error = new Error('newer error')
    const normalized = normalizeRestoredQueryState('cached', {
      dataUpdatedAt: 100,
      error,
      errorUpdatedAt: 250,
      fetchFailureCount: 4,
      fetchStatus: 'fetching',
      status: 'success',
      isInvalidated: true,
    })

    expect(normalized).toMatchObject({
      data: 'cached',
      dataUpdatedAt: 100,
      dataUpdateCount: 1,
      error,
      errorUpdatedAt: 250,
      errorUpdateCount: 1,
      fetchFailureCount: 4,
      isInvalidated: true,
      status: 'error',
      fetchStatus: 'idle',
    })

    const current = snapshotState({
      data: 'live-new',
      dataUpdatedAt: 500,
      dataUpdateCount: 5,
      error: null,
      errorUpdatedAt: 100,
      status: 'success',
      fetchStatus: 'fetching',
    })
    const persisted = snapshotState({
      data: 'persisted-old',
      dataUpdatedAt: 200,
      dataUpdateCount: 2,
      error,
      errorUpdatedAt: 400,
      errorUpdateCount: 3,
      fetchFailureCount: 6,
      fetchFailureReason: error,
      isInvalidated: true,
      status: 'error',
    })

    expect(mergeRestoredQueryState(current, persisted)).toEqual({
      data: 'live-new',
      dataUpdateCount: 5,
      dataUpdatedAt: 500,
      error,
      errorUpdateCount: 3,
      errorUpdatedAt: 400,
      fetchFailureCount: 6,
      fetchFailureReason: error,
      fetchMeta: null,
      isInvalidated: false,
      status: 'error',
      fetchStatus: 'idle',
    })

    const olderData = snapshotState({
      data: 'live-old',
      dataUpdatedAt: 200,
      dataUpdateCount: 2,
      error,
      errorUpdatedAt: 400,
      errorUpdateCount: 3,
      fetchFailureCount: 6,
      fetchFailureReason: error,
      status: 'error',
      isInvalidated: true,
    })
    const newerData = snapshotState({
      data: 'persisted-new',
      dataUpdatedAt: 500,
      dataUpdateCount: 5,
      error: null,
      errorUpdatedAt: 100,
      status: 'success',
      isInvalidated: false,
    })

    expect(mergeRestoredQueryState(olderData, newerData)).toMatchObject({
      data: 'persisted-new',
      dataUpdatedAt: 500,
      dataUpdateCount: 5,
      error,
      errorUpdatedAt: 400,
      errorUpdateCount: 3,
      fetchFailureCount: 6,
      isInvalidated: false,
      status: 'error',
      fetchStatus: 'idle',
    })

    const newest = snapshotState({
      data: 'live',
      dataUpdatedAt: 800,
      error: new Error('live'),
      errorUpdatedAt: 800,
      fetchFailureCount: 1,
      status: 'error',
      fetchStatus: 'fetching',
    })
    expect(mergeRestoredQueryState(newest, persisted)).toBe(newest)
  })
})
