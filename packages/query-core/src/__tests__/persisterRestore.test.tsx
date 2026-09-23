import { describe, expect, it, vi } from 'vitest'
import {
  QueryCache,
  QueryClient,
  QueryObserver,
  createPersisterRestoreResult,
  isPersisterRestoreResult,
} from '..'
import type { QueryState } from '..'

function restoredState(
  overrides: Partial<QueryState<unknown, Error>> = {},
): QueryState<unknown, Error> {
  return {
    data: 'cached',
    dataUpdateCount: 2,
    dataUpdatedAt: 1_700_000_000_000,
    error: null,
    errorUpdateCount: 0,
    errorUpdatedAt: 0,
    fetchFailureCount: 0,
    fetchFailureReason: null,
    fetchMeta: null,
    isInvalidated: false,
    status: 'success',
    fetchStatus: 'idle',
    ...overrides,
  }
}

describe('createPersisterRestoreResult', () => {
  it('returns a marker that identifies a persisted snapshot', () => {
    const state = restoredState()
    const marker = createPersisterRestoreResult({
      data: state.data,
      state,
    })

    expect(isPersisterRestoreResult(marker)).toBe(true)
    expect(marker.data).toBe('cached')
    expect(marker.state).toBe(state)
    expect(isPersisterRestoreResult('cached')).toBe(false)
    expect(isPersisterRestoreResult({ data: 'cached', state })).toBe(false)
  })

  it('adopts the snapshot from prefetchQuery without a success fetch', async () => {
    const onSuccess = vi.fn()
    const onSettled = vi.fn()
    const queryFn = vi.fn(() => 'fresh')
    const error = new Error('refetch failed')
    const state = restoredState({
      data: { pages: ['a'], pageParams: [1] },
      dataUpdateCount: 3,
      dataUpdatedAt: 111,
      error,
      errorUpdateCount: 2,
      errorUpdatedAt: 222,
      fetchFailureCount: 4,
      fetchFailureReason: error,
      isInvalidated: true,
      status: 'error',
      fetchStatus: 'fetching',
    })
    const client = new QueryClient({
      queryCache: new QueryCache({ onSuccess, onSettled }),
    })

    const data = await client.fetchQuery({
      queryKey: ['restored'],
      queryFn,
      persister: () =>
        createPersisterRestoreResult({
          data: state.data,
          state,
        }),
    })

    expect(data).toEqual({ pages: ['a'], pageParams: [1] })
    expect(queryFn).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(onSettled).not.toHaveBeenCalled()
    expect(client.getQueryState(['restored'])).toMatchObject({
      data: { pages: ['a'], pageParams: [1] },
      dataUpdateCount: 3,
      dataUpdatedAt: 111,
      error,
      errorUpdateCount: 2,
      errorUpdatedAt: 222,
      fetchFailureCount: 4,
      fetchFailureReason: error,
      isInvalidated: true,
      status: 'error',
      fetchStatus: 'idle',
    })

    const observer = new QueryObserver(client, {
      queryKey: ['restored'],
      queryFn,
      staleTime: Infinity,
      refetchOnMount: false,
    })
    const unsubscribe = observer.subscribe(() => {})

    expect(observer.getCurrentResult()).toMatchObject({
      data: { pages: ['a'], pageParams: [1] },
      dataUpdatedAt: 111,
      error,
      errorUpdatedAt: 222,
      failureCount: 4,
      failureReason: error,
      errorUpdateCount: 2,
      status: 'error',
      fetchStatus: 'idle',
      isRefetchError: true,
      isSuccess: false,
      isFetching: false,
    })

    unsubscribe()
    client.clear()
  })

  it('keeps persisted failure metadata when a mounted observer refetches', async () => {
    const error = new Error('still failed')
    const state = restoredState({
      dataUpdatedAt: 10,
      error,
      errorUpdateCount: 2,
      errorUpdatedAt: 20,
      fetchFailureCount: 5,
      fetchFailureReason: error,
      isInvalidated: false,
      status: 'error',
    })
    const client = new QueryClient()
    const queryFn = vi.fn(() => new Promise<string>(() => {}))

    await client.prefetchQuery({
      queryKey: ['mounted'],
      queryFn,
      persister: () =>
        createPersisterRestoreResult({
          data: 'cached',
          state,
        }),
    })

    const observer = new QueryObserver(client, {
      queryKey: ['mounted'],
      queryFn,
      staleTime: 0,
    })
    const unsubscribe = observer.subscribe(() => {})

    expect(observer.getCurrentResult()).toMatchObject({
      data: 'cached',
      dataUpdatedAt: 10,
      error,
      errorUpdatedAt: 20,
      failureCount: 5,
      failureReason: error,
      status: 'error',
      fetchStatus: 'fetching',
      isRefetchError: true,
    })

    unsubscribe()
    client.clear()
  })
})
