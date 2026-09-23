import { afterAll, beforeAll, describe, expect, test, vi } from 'vitest'
import { Query, QueryClient, hashKey } from '@tanstack/query-core'
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

  test('should restore the full persisted query state when fetching through the query', async () => {
    const storage = getFreshStorage()
    const { client, persister, queryKey, storageKey } = setupPersister(
      ['foo'],
      {
        storage,
        refetchOnRestore: false,
      },
    )
    const onSuccess = vi.fn()
    client.getQueryCache().config.onSuccess = onSuccess

    const persistedState = {
      data: 'persisted',
      dataUpdateCount: 2,
      dataUpdatedAt: Date.now() - 2000,
      error: { message: 'persisted error' },
      errorUpdateCount: 1,
      errorUpdatedAt: Date.now() - 1000,
      fetchFailureCount: 3,
      fetchFailureReason: { message: 'persisted error' },
      fetchMeta: null,
      isInvalidated: true,
      status: 'error',
      fetchStatus: 'idle',
    }

    await storage.setItem(
      storageKey,
      JSON.stringify({ buster: '', state: persistedState }),
    )

    const queryFn = vi.fn(() => 'fresh')
    await client.prefetchQuery({
      queryKey,
      queryFn,
      persister: persister.persisterFn,
    })
    await vi.advanceTimersByTimeAsync(0)

    expect(queryFn).not.toHaveBeenCalled()
    expect(onSuccess).not.toHaveBeenCalled()
    expect(client.getQueryState(queryKey)).toEqual(persistedState)
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

    function persistState(
      storage: ReturnType<typeof getFreshStorage>,
      queryKey: QueryKey,
      state: Record<string, unknown>,
    ) {
      const queryHash = hashKey(queryKey)
      return storage.setItem(
        `${PERSISTER_KEY_PREFIX}-${queryHash}`,
        JSON.stringify({ buster: '', queryHash, queryKey, state }),
      )
    }

    const now = Date.now()
    const errorState = {
      data: 'persisted',
      dataUpdateCount: 2,
      dataUpdatedAt: now - 3000,
      error: { message: 'persisted error' },
      errorUpdateCount: 1,
      errorUpdatedAt: now - 2000,
      fetchFailureCount: 3,
      fetchFailureReason: { message: 'persisted error' },
      fetchMeta: null,
      isInvalidated: true,
      status: 'error',
      fetchStatus: 'idle',
    }

    test('should restore the full state of multiple queries', async () => {
      const storage = getFreshStorage()
      const { persister, client } = setupPersister(['foo'], { storage })
      const infiniteData = { pages: ['a', 'b'], pageParams: [0, 1] }
      const infiniteState = {
        ...errorState,
        data: infiniteData,
        error: null,
        errorUpdateCount: 0,
        errorUpdatedAt: 0,
        fetchFailureCount: 0,
        fetchFailureReason: null,
        isInvalidated: false,
        status: 'success',
      }

      await persistState(storage, ['error'], errorState)
      await persistState(storage, ['infinite'], infiniteState)

      await persister.restoreQueries(client)

      expect(client.getQueryCache().getAll()).toHaveLength(2)
      expect(client.getQueryState(['error'])).toEqual(errorState)
      expect(client.getQueryState(['infinite'])).toEqual(infiniteState)
    })

    test('should keep newer live data while adopting a newer persisted error', async () => {
      const storage = getFreshStorage()
      const { persister, client } = setupPersister(['foo'], { storage })

      client.setQueryData(['error'], 'live', { updatedAt: now - 1000 })
      await persistState(storage, ['error'], errorState)

      await persister.restoreQueries(client)

      expect(client.getQueryState(['error'])).toMatchObject({
        data: 'live',
        dataUpdatedAt: now - 1000,
        dataUpdateCount: 1,
        error: errorState.error,
        errorUpdatedAt: errorState.errorUpdatedAt,
        errorUpdateCount: errorState.errorUpdateCount,
        fetchFailureCount: errorState.fetchFailureCount,
        isInvalidated: true,
        status: 'error',
        fetchStatus: 'idle',
      })
    })

    test('should keep newer persisted data while preserving a newer live error', async () => {
      const storage = getFreshStorage()
      const { persister, client } = setupPersister(['foo'], { storage })
      const liveError = new Error('live error')

      client.setQueryData(['error'], 'live', { updatedAt: now - 5000 })
      const query = client.getQueryCache().find({ queryKey: ['error'] })!
      query.setState({
        error: liveError,
        errorUpdatedAt: now - 500,
        errorUpdateCount: 4,
        fetchFailureCount: 1,
        fetchFailureReason: liveError,
        status: 'error',
        isInvalidated: true,
      })
      await persistState(storage, ['error'], errorState)

      await persister.restoreQueries(client)

      expect(client.getQueryState(['error'])).toMatchObject({
        data: 'persisted',
        dataUpdatedAt: errorState.dataUpdatedAt,
        dataUpdateCount: errorState.dataUpdateCount,
        error: liveError,
        errorUpdatedAt: now - 500,
        errorUpdateCount: 4,
        fetchFailureCount: 1,
        isInvalidated: true,
        status: 'error',
      })
    })

    test('should not touch a live query that is newer on both sides', async () => {
      const storage = getFreshStorage()
      const { persister, client } = setupPersister(['foo'], { storage })

      client.setQueryData(['error'], 'live')
      client
        .getQueryCache()
        .find({ queryKey: ['error'] })!
        .setState({ errorUpdatedAt: now - 1000 })
      const liveState = client.getQueryState(['error'])
      await persistState(storage, ['error'], errorState)

      await persister.restoreQueries(client)

      expect(client.getQueryState(['error'])).toBe(liveState)
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
