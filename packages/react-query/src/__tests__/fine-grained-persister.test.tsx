import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as React from 'react'
import {
  PERSISTER_KEY_PREFIX,
  experimental_createQueryPersister,
} from '@tanstack/query-persist-client-core'
import { queryKey, sleep } from '@tanstack/query-test-utils'
import { QueryCache, QueryClient, hashKey, useQuery } from '..'
import { renderWithClient } from './utils'
import type { QueryKey, QueryState, UseQueryResult } from '..'

describe('fine grained persister', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  const queryCache = new QueryCache()
  const queryClient = new QueryClient({ queryCache })

  it('should restore query state from persister and not refetch', async () => {
    const key = queryKey()
    const hash = hashKey(key)
    const spy = vi.fn(() => Promise.resolve('Works from queryFn'))

    const mapStorage = new Map()
    const storage = {
      getItem: (itemKey: string) => Promise.resolve(mapStorage.get(itemKey)),
      setItem: (itemKey: string, value: unknown) => {
        mapStorage.set(itemKey, value)
        return Promise.resolve()
      },
      removeItem: (itemKey: string) => {
        mapStorage.delete(itemKey)
        return Promise.resolve()
      },
    }

    await storage.setItem(
      `${PERSISTER_KEY_PREFIX}-${hash}`,
      JSON.stringify({
        buster: '',
        queryHash: hash,
        queryKey: key,
        state: {
          dataUpdatedAt: Date.now(),
          data: 'Works from persister',
        },
      }),
    )

    function Test() {
      const [_, setRef] = React.useState<HTMLDivElement | null>()

      const { data } = useQuery({
        queryKey: key,
        queryFn: spy,
        persister: experimental_createQueryPersister({
          storage,
        }).persisterFn,
        staleTime: 5000,
      })

      return <div ref={(value) => setRef(value)}>{data}</div>
    }

    const rendered = renderWithClient(queryClient, <Test />)

    await vi.advanceTimersByTimeAsync(0)
    expect(rendered.getByText('Works from persister')).toBeInTheDocument()
    expect(spy).not.toHaveBeenCalled()
  })

  it('should restore query state from persister and refetch', async () => {
    const key = queryKey()
    const hash = hashKey(key)
    const spy = vi.fn(async () => {
      await sleep(5)

      return 'Works from queryFn'
    })

    const mapStorage = new Map()
    const storage = {
      getItem: (itemKey: string) => Promise.resolve(mapStorage.get(itemKey)),
      setItem: (itemKey: string, value: unknown) => {
        mapStorage.set(itemKey, value)
        return Promise.resolve()
      },
      removeItem: (itemKey: string) => {
        mapStorage.delete(itemKey)
        return Promise.resolve()
      },
    }

    await storage.setItem(
      `${PERSISTER_KEY_PREFIX}-${hash}`,
      JSON.stringify({
        buster: '',
        queryHash: hash,
        queryKey: key,
        state: {
          dataUpdatedAt: Date.now(),
          data: 'Works from persister',
        },
      }),
    )

    function Test() {
      const [_, setRef] = React.useState<HTMLDivElement | null>()

      const { data } = useQuery({
        queryKey: key,
        queryFn: spy,
        persister: experimental_createQueryPersister({
          storage,
        }).persisterFn,
      })

      return <div ref={(value) => setRef(value)}>{data}</div>
    }

    const rendered = renderWithClient(queryClient, <Test />)

    await vi.advanceTimersByTimeAsync(0)
    expect(rendered.getByText('Works from persister')).toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(6)
    expect(rendered.getByText('Works from queryFn')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledTimes(1)
  })

  it('should store query state to persister after fetch', async () => {
    const key = queryKey()
    const hash = hashKey(key)
    const spy = vi.fn(() => Promise.resolve('Works from queryFn'))

    const mapStorage = new Map()
    const storage = {
      getItem: (itemKey: string) => Promise.resolve(mapStorage.get(itemKey)),
      setItem: (itemKey: string, value: unknown) => {
        mapStorage.set(itemKey, value)
        return Promise.resolve()
      },
      removeItem: (itemKey: string) => {
        mapStorage.delete(itemKey)
        return Promise.resolve()
      },
    }

    function Test() {
      const [_, setRef] = React.useState<HTMLDivElement | null>()

      const { data } = useQuery({
        queryKey: key,
        queryFn: spy,
        persister: experimental_createQueryPersister({
          storage,
        }).persisterFn,
      })

      return <div ref={(value) => setRef(value)}>{data}</div>
    }

    const rendered = renderWithClient(queryClient, <Test />)

    await vi.advanceTimersByTimeAsync(0)
    expect(rendered.getByText('Works from queryFn')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledTimes(1)

    const storedItem = await storage.getItem(`${PERSISTER_KEY_PREFIX}-${hash}`)
    expect(JSON.parse(storedItem)).toMatchObject({
      state: {
        data: 'Works from queryFn',
      },
    })
  })

  describe('restored query state', () => {
    const persistedError = { message: 'Persisted error' }

    function createStorage() {
      const mapStorage = new Map<string, string>()
      return {
        getItem: (itemKey: string) => Promise.resolve(mapStorage.get(itemKey)),
        setItem: (itemKey: string, value: string) => {
          mapStorage.set(itemKey, value)
          return Promise.resolve()
        },
        removeItem: (itemKey: string) => {
          mapStorage.delete(itemKey)
          return Promise.resolve()
        },
        entries: () => Promise.resolve(Array.from(mapStorage.entries())),
      }
    }

    function storeQuery(
      storage: ReturnType<typeof createStorage>,
      key: QueryKey,
      state: QueryState<string, typeof persistedError>,
    ) {
      const hash = hashKey(key)
      return storage.setItem(
        `${PERSISTER_KEY_PREFIX}-${hash}`,
        JSON.stringify({ buster: '', queryHash: hash, queryKey: key, state }),
      )
    }

    function refetchErrorState(now: number) {
      return {
        data: 'Works from persister',
        dataUpdateCount: 1,
        dataUpdatedAt: now - 2000,
        error: persistedError,
        errorUpdateCount: 1,
        errorUpdatedAt: now - 1000,
        fetchFailureCount: 2,
        fetchFailureReason: persistedError,
        fetchMeta: null,
        isInvalidated: true,
        status: 'error',
        fetchStatus: 'idle',
      } satisfies QueryState<string, typeof persistedError>
    }

    it('should expose the persisted error state and metadata after restoring', async () => {
      const key = queryKey()
      const spy = vi.fn(() => Promise.resolve('Works from queryFn'))
      const storage = createStorage()
      const now = Date.now()
      const persister = experimental_createQueryPersister({
        storage,
        refetchOnRestore: false,
      })

      await storeQuery(storage, key, refetchErrorState(now))

      const states: Array<UseQueryResult<string, typeof persistedError>> = []

      function Test() {
        const state = useQuery<string, typeof persistedError>({
          queryKey: key,
          queryFn: spy,
          persister: persister.persisterFn,
        })
        states.push(state)

        return <div>{state.data}</div>
      }

      const rendered = renderWithClient(queryClient, <Test />)

      await vi.advanceTimersByTimeAsync(0)
      expect(rendered.getByText('Works from persister')).toBeInTheDocument()
      expect(states.at(-1)).toMatchObject({
        data: 'Works from persister',
        dataUpdatedAt: now - 2000,
        error: persistedError,
        errorUpdatedAt: now - 1000,
        errorUpdateCount: 1,
        failureCount: 2,
        failureReason: persistedError,
        fetchStatus: 'idle',
        isRefetchError: true,
        isStale: true,
        status: 'error',
      })
      expect(spy).not.toHaveBeenCalled()
    })

    it('should expose the persisted state of queries restored in bulk', async () => {
      const errorKey = queryKey()
      const successKey = queryKey()
      const spy = vi.fn(() => Promise.resolve('Works from queryFn'))
      const storage = createStorage()
      const now = Date.now()
      const persister = experimental_createQueryPersister({ storage })

      await storeQuery(storage, errorKey, refetchErrorState(now))
      await storeQuery(storage, successKey, {
        ...refetchErrorState(now),
        data: 'Works from persister too',
        dataUpdatedAt: now - 500,
        error: null,
        fetchFailureCount: 0,
        fetchFailureReason: null,
        status: 'success',
      })

      await persister.restoreQueries(queryClient)

      const results: Array<
        Array<UseQueryResult<string, typeof persistedError>>
      > = []

      function Test() {
        const errorQuery = useQuery<string, typeof persistedError>({
          queryKey: errorKey,
          queryFn: spy,
          refetchOnMount: false,
        })
        const successQuery = useQuery<string, typeof persistedError>({
          queryKey: successKey,
          queryFn: spy,
          refetchOnMount: false,
        })
        results.push([errorQuery, successQuery])

        return (
          <div>
            {errorQuery.data}, {successQuery.data}
          </div>
        )
      }

      const rendered = renderWithClient(queryClient, <Test />)

      expect(
        rendered.getByText('Works from persister, Works from persister too'),
      ).toBeInTheDocument()
      expect(results[0]?.[0]).toMatchObject({
        dataUpdatedAt: now - 2000,
        error: persistedError,
        errorUpdatedAt: now - 1000,
        failureCount: 2,
        fetchStatus: 'idle',
        isRefetchError: true,
        status: 'error',
      })
      expect(results[0]?.[1]).toMatchObject({
        dataUpdatedAt: now - 500,
        errorUpdatedAt: now - 1000,
        errorUpdateCount: 1,
        failureCount: 0,
        fetchStatus: 'idle',
        isStale: true,
        status: 'success',
      })

      await vi.advanceTimersByTimeAsync(0)
      expect(spy).not.toHaveBeenCalled()
    })
  })
})
