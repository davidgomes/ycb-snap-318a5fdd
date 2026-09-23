import { afterAll, beforeAll, describe, expect, test, vi } from 'vitest'
import {
  Query,
  QueryClient,
  QueryObserver,
  hashKey,
} from '@tanstack/query-core'
import {
  PERSISTER_KEY_PREFIX,
  experimental_createQueryPersister,
} from '../createPersister'
import type { QueryFunctionContext, QueryKey } from '@tanstack/query-core'
import type { StoragePersisterOptions } from '../createPersister'

function getFreshStorage() {
  const storage = new Map()
  return {
    getItem: (key: string) => Promise.resolve(storage.get(key)),
    setItem: (key: string, value: unknown) => {
      storage.set(key, value)
      return Promise.resolve()
    },
    removeItem: (key: string) => {
      storage.delete(key)
      return Promise.resolve()
    },
    entries: () => {
      return Promise.resolve(Array.from(storage.entries()))
    },
  }
}

function setupPersister(
  queryKey: QueryKey,
  persisterOptions: StoragePersisterOptions,
) {
  const client = new QueryClient()
  const context = {
    meta: { foo: 'bar' },
    client,
    queryKey,
    // @ts-expect-error
    signal: undefined as AbortSignal,
  } satisfies QueryFunctionContext
  const queryHash = hashKey(queryKey)
  const storageKey = `${PERSISTER_KEY_PREFIX}-${queryHash}`

  const queryFn = vi.fn()

  const persister = experimental_createQueryPersister(persisterOptions)

  const query = new Query({
    client,
    queryHash,
    queryKey,
  })

  return {
    client,
    context,
    persister,
    query,
    queryFn,
    queryHash,
    queryKey,
    storageKey,
  }
}

describe('createPersister', () => {
  beforeAll(() => {
    vi.useFakeTimers()
  })

  afterAll(() => {
    vi.useRealTimers()
  })

  test('should fetch if storage is not provided', async () => {
    const { context, persister, query, queryFn } = setupPersister(['foo'], {
      storage: undefined,
    })

    await persister.persisterFn(queryFn, context, query)

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should fetch if there is no stored data', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn } = setupPersister(['foo'], {
      storage,
    })

    await persister.persisterFn(queryFn, context, query)

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should fetch if query already has data', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn } = setupPersister(['foo'], {
      storage,
    })
    query.state.data = 'baz'

    await persister.persisterFn(queryFn, context, query)

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should fetch if deserialization fails', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
      },
    )

    await storage.setItem(storageKey, '{invalid[item')

    await persister.persisterFn(queryFn, context, query)

    expect(await storage.getItem(storageKey)).toBeUndefined()

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should remove stored item if `dataUpdatedAt` is empty', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt: undefined },
      }),
    )

    await persister.persisterFn(queryFn, context, query)

    expect(await storage.getItem(storageKey)).toBeUndefined()

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should remove stored item if its expired', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
        maxAge: 100,
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt: Date.now() - 200 },
      }),
    )

    await persister.persisterFn(queryFn, context, query)

    expect(await storage.getItem(storageKey)).toBeUndefined()

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should remove stored item if its busted', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: 'bust',
        state: { dataUpdatedAt: Date.now() },
      }),
    )

    await persister.persisterFn(queryFn, context, query)

    expect(await storage.getItem(storageKey)).toBeUndefined()

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)
  })

  test('should restore item from the storage and set proper `updatedAt` values', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
      },
    )

    const dataUpdatedAt = Date.now()

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt, data: '' },
      }),
    )

    await persister.persisterFn(queryFn, context, query)
    query.state.data = 'data0'
    query.fetch = vi.fn()
    expect(query.state.dataUpdatedAt).toEqual(0)

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledTimes(0)
    expect(query.fetch).toHaveBeenCalledTimes(0)
    expect(query.state.dataUpdatedAt).toEqual(dataUpdatedAt)
  })

  test('should restore item from the storage and refetch when `stale`', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt: Date.now(), data: '' },
      }),
    )

    await persister.persisterFn(queryFn, context, query)
    query.state.isInvalidated = true
    query.fetch = vi.fn()

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledTimes(0)
    expect(query.fetch).toHaveBeenCalledTimes(1)
  })

  test('should restore item from the storage and refetch when `refetchOnRestore` is set to `always`', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
        refetchOnRestore: 'always',
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt: Date.now() + 1000, data: '' },
      }),
    )

    await persister.persisterFn(queryFn, context, query)
    query.state.isInvalidated = true
    query.fetch = vi.fn()

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledTimes(0)
    expect(query.fetch).toHaveBeenCalledTimes(1)
  })

  test('should restore item from the storage and NOT refetch when `refetchOnRestore` is set to false', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
        refetchOnRestore: false,
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt: Date.now(), data: '' },
      }),
    )

    await persister.persisterFn(queryFn, context, query)
    query.state.isInvalidated = true
    query.fetch = vi.fn()

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledTimes(0)
    expect(query.fetch).toHaveBeenCalledTimes(0)
  })

  test('should store item after successful fetch', async () => {
    const storage = getFreshStorage()
    const {
      context,
      persister,
      query,
      queryFn,
      queryHash,
      queryKey,
      storageKey,
    } = setupPersister(['foo'], {
      storage,
    })

    await persister.persisterFn(queryFn, context, query)
    query.setData('baz')

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)

    expect(JSON.parse(await storage.getItem(storageKey))).toMatchObject({
      buster: '',
      queryHash,
      queryKey,
      state: {
        data: 'baz',
      },
    })
  })

  test('should skip stored item if not matched by filters', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
        filters: {
          predicate: () => {
            return false
          },
        },
      },
    )

    const dataUpdatedAt = Date.now()

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt },
      }),
    )

    await persister.persisterFn(queryFn, context, query)
    query.fetch = vi.fn()

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledTimes(1)
    expect(query.fetch).toHaveBeenCalledTimes(0)
  })

  test('should restore item from the storage with async deserializer', async () => {
    const storage = getFreshStorage()
    const { context, persister, query, queryFn, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
        deserialize: (cachedString: string) =>
          new Promise((resolve) => resolve(JSON.parse(cachedString))),
      },
    )

    await storage.setItem(
      storageKey,
      JSON.stringify({
        buster: '',
        state: { dataUpdatedAt: Date.now(), data: '' },
      }),
    )

    await persister.persisterFn(queryFn, context, query)
    query.state.isInvalidated = true
    query.fetch = vi.fn()

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledTimes(0)
    expect(query.fetch).toHaveBeenCalledTimes(1)
  })

  test('should store item after successful fetch with async serializer', async () => {
    const storage = getFreshStorage()
    const {
      context,
      persister,
      query,
      queryFn,
      queryHash,
      queryKey,
      storageKey,
    } = setupPersister(['foo'], {
      storage,
      serialize: (persistedQuery) =>
        new Promise((resolve) => resolve(JSON.stringify(persistedQuery))),
    })

    await persister.persisterFn(queryFn, context, query)
    query.setData('baz')

    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).toHaveBeenCalledExactlyOnceWith(context)

    expect(JSON.parse(await storage.getItem(storageKey))).toMatchObject({
      buster: '',
      queryHash,
      queryKey,
      state: {
        data: 'baz',
      },
    })
  })

  describe('persistQuery', () => {
    test('Should properly persiste basic query', async () => {
      const storage = getFreshStorage()
      const { persister, query, queryHash, queryKey, storageKey } =
        setupPersister(['foo'], {
          storage,
        })

      query.setData('baz')
      await persister.persistQuery(query)

      expect(JSON.parse(await storage.getItem(storageKey))).toMatchObject({
        buster: '',
        queryHash,
        queryKey,
        state: {
          dataUpdateCount: 1,
          data: 'baz',
          status: 'success',
        },
      })
    })

    test('Should skip persistance if storage is not provided', async () => {
      const serializeMock = vi.fn()
      const { persister, query } = setupPersister(['foo'], {
        storage: null,
        serialize: serializeMock,
      })

      query.setData('baz')
      await persister.persistQuery(query)

      expect(serializeMock).toHaveBeenCalledTimes(0)
    })
  })

  describe('persistQueryByKey', () => {
    test('Should skip persistance if storage is not provided', async () => {
      const serializeMock = vi.fn()
      const { persister, client, queryKey } = setupPersister(['foo'], {
        storage: null,
        serialize: serializeMock,
      })

      client.setQueryData(queryKey, 'baz')
      await persister.persistQueryByKey(queryKey, client)

      expect(serializeMock).toHaveBeenCalledTimes(0)
    })

    test('should skip persistance if query was not found', async () => {
      const serializeMock = vi.fn()
      const storage = getFreshStorage()
      const { client, persister, queryKey } = setupPersister(['foo'], {
        storage,
        serialize: serializeMock,
      })

      client.setQueryData(queryKey, 'baz')
      await persister.persistQueryByKey(['foo2'], client)

      expect(serializeMock).toHaveBeenCalledTimes(0)
    })

    test('Should properly persiste basic query', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryHash, queryKey, storageKey } =
        setupPersister(['foo'], {
          storage,
        })

      client.setQueryData(queryKey, 'baz')
      await persister.persistQueryByKey(queryKey, client)

      expect(JSON.parse(await storage.getItem(storageKey))).toMatchObject({
        buster: '',
        queryHash,
        queryKey,
        state: {
          dataUpdateCount: 1,
          data: 'baz',
          status: 'success',
        },
      })
    })
  })

  describe('persisterGc', () => {
    test('should properly clean storage from busted entries', async () => {
      const storage = getFreshStorage()
      const { persister, client, query, queryKey } = setupPersister(['foo'], {
        storage,
      })
      query.setState({
        dataUpdatedAt: 1,
        data: 'f',
      })
      client.getQueryCache().add(query)

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)

      await persister.persisterGc()
      expect(await storage.entries()).toHaveLength(0)
    })
  })

  describe('restoreQueries', () => {
    test('should properly clean storage from busted entries', async () => {
      const storage = getFreshStorage()
      const { persister, client, query, queryKey } = setupPersister(['foo'], {
        storage,
      })
      query.setState({
        dataUpdatedAt: 1,
        data: 'f',
      })
      client.getQueryCache().add(query)

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)

      await persister.restoreQueries(client)
      expect(await storage.entries()).toHaveLength(0)
    })

    test('should preserve failure metadata and pagination when restoring many queries', async () => {
      const storage = getFreshStorage()
      const { persister, client } = setupPersister(['foo'], {
        storage,
        maxAge: Number.POSITIVE_INFINITY,
      })
      const error = { message: 'persisted failure' }
      const firstKey = ['todos', 1]
      const secondKey = ['todos', 2]
      const firstHash = hashKey(firstKey)
      const secondHash = hashKey(secondKey)
      const sharedState = {
        dataUpdateCount: 3,
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
      }

      await storage.setItem(
        `${PERSISTER_KEY_PREFIX}-${firstHash}`,
        JSON.stringify({
          buster: '',
          queryHash: firstHash,
          queryKey: firstKey,
          state: {
            ...sharedState,
            data: 'alpha',
            error,
            errorUpdateCount: 4,
            errorUpdatedAt: 1_700_000_000_500,
            fetchFailureCount: 4,
            fetchFailureReason: error,
            isInvalidated: true,
            status: 'error',
          },
        }),
      )
      await storage.setItem(
        `${PERSISTER_KEY_PREFIX}-${secondHash}`,
        JSON.stringify({
          buster: '',
          queryHash: secondHash,
          queryKey: secondKey,
          state: {
            ...sharedState,
            data: {
              pages: ['p1', 'p2'],
              pageParams: [1, 2],
            },
            dataUpdateCount: 6,
            dataUpdatedAt: 1_700_000_000_800,
          },
        }),
      )

      await persister.restoreQueries(client)

      const first = client.getQueryCache().find({ queryKey: firstKey })!
      const second = client.getQueryCache().find({ queryKey: secondKey })!

      expect(first.state).toMatchObject({
        data: 'alpha',
        status: 'error',
        fetchStatus: 'idle',
        error,
        errorUpdatedAt: 1_700_000_000_500,
        errorUpdateCount: 4,
        fetchFailureCount: 4,
        dataUpdatedAt: 1_700_000_000_000,
        dataUpdateCount: 3,
        isInvalidated: true,
      })
      expect(second.state.data).toEqual({
        pages: ['p1', 'p2'],
        pageParams: [1, 2],
      })
      expect(second.state).toMatchObject({
        status: 'success',
        fetchStatus: 'idle',
        dataUpdatedAt: 1_700_000_000_800,
        dataUpdateCount: 6,
        fetchFailureCount: 0,
      })

      const observer = new QueryObserver(client, {
        queryKey: firstKey,
        queryFn: () => 'unused',
        refetchOnMount: false,
        staleTime: Infinity,
      })
      const unsubscribe = observer.subscribe(() => {})

      expect(observer.getCurrentResult()).toMatchObject({
        status: 'error',
        fetchStatus: 'idle',
        data: 'alpha',
        error,
        isRefetchError: true,
        failureCount: 4,
        errorUpdatedAt: 1_700_000_000_500,
        dataUpdatedAt: 1_700_000_000_000,
        errorUpdateCount: 4,
      })

      unsubscribe()
    })

    test('should keep newer data and newer error metadata when they come from different sides', async () => {
      const storage = getFreshStorage()
      const {
        persister,
        client,
        queryKey: key,
      } = setupPersister(['foo'], {
        storage,
        maxAge: Number.POSITIVE_INFINITY,
      })
      const liveError = new Error('live failure')
      const persistedError = {
        name: 'Error',
        message: 'persisted failure',
      }

      client.setQueryData(key, 'live-data', { updatedAt: 500 })
      const liveQuery = client.getQueryCache().find({ queryKey: key })!
      liveQuery.setState({
        data: 'live-data',
        dataUpdatedAt: 500,
        dataUpdateCount: 2,
        error: null,
        errorUpdatedAt: 20,
        errorUpdateCount: 0,
        fetchFailureCount: 0,
        fetchFailureReason: null,
        status: 'success',
        fetchStatus: 'idle',
        isInvalidated: false,
      })

      const hash = hashKey(key)
      await storage.setItem(
        `${PERSISTER_KEY_PREFIX}-${hash}`,
        JSON.stringify({
          buster: '',
          queryHash: hash,
          queryKey: key,
          state: {
            data: 'old-data',
            dataUpdatedAt: 100,
            dataUpdateCount: 1,
            error: persistedError,
            errorUpdatedAt: 400,
            errorUpdateCount: 3,
            fetchFailureCount: 3,
            fetchFailureReason: persistedError,
            fetchMeta: null,
            isInvalidated: true,
            status: 'error',
            fetchStatus: 'idle',
          },
        }),
      )

      await persister.restoreQueries(client)

      expect(liveQuery.state).toMatchObject({
        data: 'live-data',
        dataUpdatedAt: 500,
        dataUpdateCount: 2,
        error: persistedError,
        errorUpdatedAt: 400,
        errorUpdateCount: 3,
        fetchFailureCount: 3,
        status: 'error',
        fetchStatus: 'idle',
        isInvalidated: true,
      })

      const newerData = {
        data: 'persisted-data',
        dataUpdatedAt: 800,
        dataUpdateCount: 8,
        error: null,
        errorUpdatedAt: 10,
        errorUpdateCount: 0,
        fetchFailureCount: 0,
        fetchFailureReason: null,
        fetchMeta: null,
        isInvalidated: false,
        status: 'success',
        fetchStatus: 'idle',
      }
      await storage.setItem(
        `${PERSISTER_KEY_PREFIX}-${hash}`,
        JSON.stringify({
          buster: '',
          queryHash: hash,
          queryKey: key,
          state: newerData,
        }),
      )
      liveQuery.setState({
        data: 'stale-live',
        dataUpdatedAt: 100,
        dataUpdateCount: 1,
        error: liveError,
        errorUpdatedAt: 900,
        errorUpdateCount: 6,
        fetchFailureCount: 6,
        fetchFailureReason: liveError,
        status: 'error',
        fetchStatus: 'idle',
        isInvalidated: true,
      })

      await persister.restoreQueries(client)

      expect(liveQuery.state).toMatchObject({
        data: 'persisted-data',
        dataUpdatedAt: 800,
        dataUpdateCount: 8,
        error: liveError,
        errorUpdatedAt: 900,
        errorUpdateCount: 6,
        fetchFailureCount: 6,
        status: 'error',
        fetchStatus: 'idle',
      })
    })

    test('should properly restore queries from cache without filters', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      client.clear()
      expect(client.getQueryCache().getAll()).toHaveLength(0)

      await persister.restoreQueries(client)
      expect(client.getQueryCache().getAll()).toHaveLength(1)

      expect(client.getQueryData(queryKey)).toEqual('foo')
    })

    test('should properly restore queries from cache', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      client.clear()
      expect(client.getQueryCache().getAll()).toHaveLength(0)

      await persister.restoreQueries(client, { queryKey })
      expect(client.getQueryCache().getAll()).toHaveLength(1)

      expect(client.getQueryData(queryKey)).toEqual('foo')
    })

    test('should not restore queries from cache if there is no match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      client.clear()
      expect(client.getQueryCache().getAll()).toHaveLength(0)

      await persister.restoreQueries(client, { queryKey: ['bar'] })
      expect(client.getQueryCache().getAll()).toHaveLength(0)
    })

    test('should properly restore queries from cache with partial match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      client.clear()
      expect(client.getQueryCache().getAll()).toHaveLength(0)

      await persister.restoreQueries(client, { queryKey: ['foo'] })
      expect(client.getQueryCache().getAll()).toHaveLength(1)

      expect(client.getQueryData(queryKey)).toEqual('foo')
    })

    test('should not restore queries from cache with exact match if there is no match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      client.clear()
      expect(client.getQueryCache().getAll()).toHaveLength(0)

      await persister.restoreQueries(client, { queryKey: ['foo'], exact: true })
      expect(client.getQueryCache().getAll()).toHaveLength(0)
    })

    test('should restore queries from cache with exact match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      client.clear()
      expect(client.getQueryCache().getAll()).toHaveLength(0)

      await persister.restoreQueries(client, {
        queryKey: queryKey,
        exact: true,
      })
      expect(client.getQueryCache().getAll()).toHaveLength(1)
    })
  })

  describe('removeQueries', () => {
    test('should remove restore queries from storage without filters', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      await persister.removeQueries()
      expect(await storage.entries()).toHaveLength(0)
    })

    test('should remove queries from storage', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      await persister.removeQueries({ queryKey })
      expect(await storage.entries()).toHaveLength(0)
    })

    test('should not remove queries from storage if there is no match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      await persister.removeQueries({ queryKey: ['bar'] })
      expect(await storage.entries()).toHaveLength(1)
    })

    test('should properly remove queries from storage with partial match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      await persister.removeQueries({ queryKey: ['foo'] })
      expect(await storage.entries()).toHaveLength(0)
    })

    test('should not remove queries from storage with exact match if there is no match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      await persister.removeQueries({ queryKey: ['foo'], exact: true })
      expect(await storage.entries()).toHaveLength(1)
    })

    test('should remove queries from storage with exact match', async () => {
      const storage = getFreshStorage()
      const { persister, client, queryKey } = setupPersister(['foo', 'bar'], {
        storage,
      })
      client.setQueryData(queryKey, 'foo')

      await persister.persistQueryByKey(queryKey, client)

      expect(await storage.entries()).toHaveLength(1)
      await persister.removeQueries({
        queryKey: queryKey,
        exact: true,
      })
      expect(await storage.entries()).toHaveLength(0)
    })
  })
})
