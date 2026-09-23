import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { queryKey } from '@tanstack/query-test-utils'
import {
  InfiniteQueryObserver,
  QueryClient,
  QueryObserver,
  createPersisterRestoreResult,
} from '..'
import type { QueryState } from '..'

describe('createPersisterRestoreResult', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2024-01-01T00:00:00.000Z'))
    queryClient = new QueryClient()
    queryClient.mount()
  })

  afterEach(() => {
    queryClient.clear()
    vi.useRealTimers()
  })

  test('adopts persisted state without success callbacks', async () => {
    const key = queryKey()
    const error = new Error('persisted refetch error')
    const onSuccess = vi.fn()
    const onSettled = vi.fn()
    const onError = vi.fn()
    queryClient.getQueryCache().config.onSuccess = onSuccess
    queryClient.getQueryCache().config.onSettled = onSettled
    queryClient.getQueryCache().config.onError = onError

    const state = {
      data: { pages: ['a'], pageParams: [1] },
      dataUpdateCount: 3,
      dataUpdatedAt: 111,
      error,
      errorUpdateCount: 2,
      errorUpdatedAt: 222,
      fetchFailureCount: 4,
      fetchFailureReason: error,
      fetchMeta: { fetchMore: { direction: 'forward' as const } },
      isInvalidated: false,
      status: 'error' as const,
      fetchStatus: 'fetching' as const,
    } satisfies QueryState

    const queryFn = vi.fn()

    await queryClient.prefetchQuery({
      queryKey: key,
      queryFn,
      persister: () =>
        createPersisterRestoreResult({
          data: state.data,
          state,
        }),
    })

    expect(queryFn).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(onSettled).not.toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()

    const query = queryClient.getQueryCache().find({ queryKey: key })!
    expect(query.state).toMatchObject({
      ...state,
      fetchStatus: 'idle',
      fetchMeta: null,
    })

    const observer = new QueryObserver(queryClient, {
      queryKey: key,
      queryFn,
      staleTime: Infinity,
    })
    const unsubscribe = observer.subscribe(vi.fn())
    const result = observer.getCurrentResult()

    expect(result).toMatchObject({
      status: 'error',
      fetchStatus: 'idle',
      data: state.data,
      dataUpdatedAt: 111,
      error,
      errorUpdatedAt: 222,
      failureCount: 4,
      failureReason: error,
      errorUpdateCount: 2,
      isError: true,
      isRefetchError: true,
      isSuccess: false,
      isStale: false,
    })

    unsubscribe()
  })

  test('keeps infinite query page params and refetch error flags', async () => {
    const key = queryKey()
    const error = new Error('page refetch')
    const data = { pages: ['one', 'two'], pageParams: [0, 1] }
    const state = {
      data,
      dataUpdateCount: 2,
      dataUpdatedAt: 10,
      error,
      errorUpdateCount: 1,
      errorUpdatedAt: 20,
      fetchFailureCount: 1,
      fetchFailureReason: error,
      fetchMeta: null,
      isInvalidated: false,
      status: 'error' as const,
      fetchStatus: 'idle' as const,
    } satisfies QueryState<typeof data>

    await queryClient.prefetchInfiniteQuery({
      queryKey: key,
      queryFn: vi.fn(),
      initialPageParam: 0,
      getNextPageParam: () => 2,
      persister: () =>
        createPersisterRestoreResult({
          data,
          state,
        }),
    })

    const observer = new InfiniteQueryObserver(queryClient, {
      queryKey: key,
      queryFn: vi.fn(),
      initialPageParam: 0,
      getNextPageParam: () => 2,
      staleTime: Infinity,
    })
    const unsubscribe = observer.subscribe(vi.fn())
    const result = observer.getCurrentResult()

    expect(result.data).toEqual(data)
    expect(result.isRefetchError).toBe(true)
    expect(result.failureCount).toBe(1)
    expect(result.errorUpdatedAt).toBe(20)
    expect(result.fetchStatus).toBe('idle')
    expect(result.hasNextPage).toBe(true)

    unsubscribe()
  })
})
