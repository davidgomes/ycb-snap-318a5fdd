import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as React from 'react'
import {
  PERSISTER_KEY_PREFIX,
  experimental_createQueryPersister,
} from '@tanstack/query-persist-client-core'
import { queryKey, sleep } from '@tanstack/query-test-utils'
import { QueryCache, QueryClient, hashKey, useQuery } from '..'
import { renderWithClient } from './utils'

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

  it('should expose a restored refetch error through useQuery', async () => {
    const key = queryKey()
    const hash = hashKey(key)
    const error = { message: 'refetch failed' }
    const client = new QueryClient()
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
    const persister = experimental_createQueryPersister({
      storage,
      maxAge: Infinity,
      refetchOnRestore: false,
    })

    await storage.setItem(
      `${PERSISTER_KEY_PREFIX}-${hash}`,
      JSON.stringify({
        buster: '',
        queryHash: hash,
        queryKey: key,
        state: {
          data: { pages: ['a', 'b'], pageParams: [0, 1] },
          dataUpdatedAt: 100,
          dataUpdateCount: 4,
          error,
          errorUpdatedAt: 180,
          errorUpdateCount: 2,
          fetchFailureCount: 3,
          fetchFailureReason: error,
          fetchMeta: null,
          isInvalidated: true,
          status: 'error',
          fetchStatus: 'fetching',
        },
      }),
    )

    await persister.restoreQueries(client)

    function Test() {
      const result = useQuery({
        queryKey: key,
        queryFn: () => 'fresh',
        staleTime: 'static',
      })

      return (
        <div>
          <span>{JSON.stringify(result.data)}</span>
          <span>failure:{result.failureCount}</span>
          <span>updated:{result.dataUpdatedAt}</span>
          <span>errorUpdated:{result.errorUpdatedAt}</span>
          <span>refetchError:{String(result.isRefetchError)}</span>
          <span>status:{result.status}</span>
          <span>fetch:{result.fetchStatus}</span>
        </div>
      )
    }

    const rendered = renderWithClient(client, <Test />)

    expect(
      rendered.getByText(
        JSON.stringify({ pages: ['a', 'b'], pageParams: [0, 1] }),
      ),
    ).toBeInTheDocument()
    expect(rendered.getByText('failure:3')).toBeInTheDocument()
    expect(rendered.getByText('updated:100')).toBeInTheDocument()
    expect(rendered.getByText('errorUpdated:180')).toBeInTheDocument()
    expect(rendered.getByText('refetchError:true')).toBeInTheDocument()
    expect(rendered.getByText('status:error')).toBeInTheDocument()
    expect(rendered.getByText('fetch:idle')).toBeInTheDocument()
  })
})
