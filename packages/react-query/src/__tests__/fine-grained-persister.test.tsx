import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as React from 'react'
import {
  PERSISTER_KEY_PREFIX,
  experimental_createQueryPersister,
} from '@tanstack/query-persist-client-core'
import { queryKey, sleep } from '@tanstack/query-test-utils'
import { QueryCache, QueryClient, hashKey, useQuery } from '..'
import { renderWithClient } from './utils'
import type { UseQueryResult } from '..'

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

  it('should expose the persisted refetch error state after restoring', async () => {
    const key = queryKey()
    const hash = hashKey(key)
    const spy = vi.fn(() => Promise.resolve('Works from queryFn'))
    const dataUpdatedAt = Date.now() - 2000
    const errorUpdatedAt = Date.now() - 1000

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
          data: 'Works from persister',
          dataUpdateCount: 1,
          dataUpdatedAt,
          error: { message: 'persisted error' },
          errorUpdateCount: 1,
          errorUpdatedAt,
          fetchFailureCount: 2,
          fetchFailureReason: { message: 'persisted error' },
          fetchMeta: null,
          isInvalidated: true,
          status: 'error',
          fetchStatus: 'idle',
        },
      }),
    )

    const results: Array<UseQueryResult<string>> = []

    function Test() {
      const result = useQuery({
        queryKey: key,
        queryFn: spy,
        persister: experimental_createQueryPersister({
          storage,
          refetchOnRestore: false,
        }).persisterFn,
      })
      results.push(result)

      return <div>{result.data}</div>
    }

    const rendered = renderWithClient(queryClient, <Test />)

    await vi.advanceTimersByTimeAsync(0)
    expect(rendered.getByText('Works from persister')).toBeInTheDocument()
    expect(spy).not.toHaveBeenCalled()
    expect(results.at(-1)).toMatchObject({
      status: 'error',
      fetchStatus: 'idle',
      isRefetchError: true,
      error: { message: 'persisted error' },
      failureCount: 2,
      dataUpdatedAt,
      errorUpdatedAt,
    })
  })

  it('should expose the persisted state of queries restored in bulk', async () => {
    const key = queryKey()
    const hash = hashKey(key)
    const spy = vi.fn(() => Promise.resolve('Works from queryFn'))
    const dataUpdatedAt = Date.now() - 2000
    const errorUpdatedAt = Date.now() - 1000

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
      entries: () => Promise.resolve(Array.from(mapStorage.entries())),
    }

    await storage.setItem(
      `${PERSISTER_KEY_PREFIX}-${hash}`,
      JSON.stringify({
        buster: '',
        queryHash: hash,
        queryKey: key,
        state: {
          data: 'Works from persister',
          dataUpdateCount: 1,
          dataUpdatedAt,
          error: { message: 'persisted error' },
          errorUpdateCount: 1,
          errorUpdatedAt,
          fetchFailureCount: 2,
          fetchFailureReason: { message: 'persisted error' },
          fetchMeta: null,
          isInvalidated: true,
          status: 'error',
          fetchStatus: 'idle',
        },
      }),
    )

    const persister = experimental_createQueryPersister({ storage })
    await persister.restoreQueries(queryClient)

    const results: Array<UseQueryResult<string>> = []

    function Test() {
      const result = useQuery({
        queryKey: key,
        queryFn: spy,
        persister: persister.persisterFn,
        refetchOnMount: false,
      })
      results.push(result)

      return <div>{result.data}</div>
    }

    const rendered = renderWithClient(queryClient, <Test />)

    await vi.advanceTimersByTimeAsync(0)
    expect(rendered.getByText('Works from persister')).toBeInTheDocument()
    expect(spy).not.toHaveBeenCalled()
    expect(results[0]).toMatchObject({
      status: 'error',
      fetchStatus: 'idle',
      isRefetchError: true,
      failureCount: 2,
      dataUpdatedAt,
      errorUpdatedAt,
    })
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
})
