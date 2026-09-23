import * as t from 'lib0/testing'
import * as Y from '../src/index.js'

/**
 * @typedef {import('../src/utils/MapConflicts.js').MapConflict} MapConflict
 */

/**
 * @param {function():void} f
 * @return {Y.MapConflictError}
 */
const catchMapConflictError = f => {
  /**
   * @type {any}
   */
  let caught = null
  try {
    f()
  } catch (err) {
    caught = err
  }
  t.assert(caught instanceof Y.MapConflictError, 'expected a MapConflictError')
  return caught
}

/**
 * @param {MapConflict} conflict
 */
const assertConflictShape = conflict => {
  t.assert(typeof conflict.key === 'string')
  t.assert(typeof conflict.parentId === 'string' && conflict.parentId.length > 0)
  t.assert(['set-set', 'delete-set', 'ambiguous'].includes(conflict.type))
  t.assert(conflict.ambiguous === (conflict.type === 'ambiguous'))
  t.assert(['local', 'remote', 'mixed'].includes(conflict.source))
  t.assert(typeof conflict.message === 'string' && conflict.message.includes(JSON.stringify(conflict.key)))
  t.assert(conflict.writes.length >= 2)
  conflict.writes.forEach(write => {
    t.assert(write.op === 'set' || write.op === 'delete')
    t.assert(write.key === conflict.key && write.parentId === conflict.parentId)
    t.assert(typeof write.snapshot.summary === 'string' && write.snapshot.summary.length > 0)
  })
  const { winner, strategy, deterministic } = conflict.resolution
  t.assert(winner !== null && winner.snapshot.summary.length > 0)
  t.assert(typeof strategy === 'string' && strategy.length > 0)
  t.assert(deterministic === true)
}

/**
 * @param {number} clientID
 * @param {function(Y.Type):void} f
 * @return {Uint8Array<ArrayBuffer>}
 */
const createUpdate = (clientID, f) => {
  const doc = new Y.Doc()
  doc.clientID = clientID
  f(doc.get('map'))
  return Y.encodeStateAsUpdate(doc)
}

/**
 * @param {Y.Doc} doc
 * @return {Array<Uint8Array<ArrayBuffer>>}
 */
const recordUpdates = doc => {
  /**
   * @type {Array<Uint8Array<ArrayBuffer>>}
   */
  const updates = []
  doc.on('update', update => { updates.push(update) })
  return updates
}

/**
 * @param {t.TestCase} _tc
 */
export const testAllowPolicy = _tc => {
  const merged = Y.mergeUpdates([
    createUpdate(1, map => map.setAttr('k', 'one')),
    createUpdate(2, map => map.setAttr('k', 'two'))
  ])
  for (const doc of [new Y.Doc(), new Y.Doc({ mapConflictPolicy: 'allow' })]) {
    t.assert(doc.mapConflictPolicy === 'allow')
    const map = doc.get('map')
    doc.transact(() => {
      map.setAttr('a', 1)
      map.setAttr('a', 2)
      map.deleteAttr('a')
      map.setAttr('a', 3)
    })
    Y.applyUpdate(doc, merged)
    t.assert(map.getAttr('a') === 3 && map.getAttr('k') === 'two')
    t.compare(doc.getMapConflicts(), [])
    t.assert(doc.getMapConflictSummary().count === 0)
  }
  t.fails(() => new Y.Doc({ mapConflictPolicy: /** @type {any} */ ('strict') }))
}

/**
 * @param {t.TestCase} _tc
 */
export const testLocalSetSet = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('map')
  doc.transact(() => {
    map.setAttr('title', 'draft')
    map.setAttr('title', 'final')
  })
  t.assert(map.getAttr('title') === 'final')
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  const [conflict] = conflicts
  assertConflictShape(conflict)
  t.assert(conflict.type === 'set-set' && conflict.kind === 'set-set' && !conflict.ambiguous)
  t.assert(conflict.key === 'title' && conflict.parentId === 'root:map' && conflict.source === 'local')
  t.compare(conflict.writes.map(write => write.snapshot.summary), ['set "title" = "draft"', 'set "title" = "final"'])
  t.compare(conflict.writes.map(write => write.source), ['local', 'local'])
  t.assert(conflict.resolution.winner === conflict.writes[1])
  t.assert(conflict.resolution.strategy === 'last-write-wins')
  // the returned array is a copy
  doc.getMapConflicts().pop()
  t.assert(doc.getMapConflicts().length === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testLocalDeleteSet = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('map')
  map.setAttr('a', 1)
  doc.transact(() => {
    map.deleteAttr('a')
    map.setAttr('a', 2)
  })
  doc.transact(() => {
    map.setAttr('b', 1)
    map.deleteAttr('b')
  })
  t.assert(map.getAttr('a') === 2 && !map.hasAttr('b'))
  const [deleteThenSet, setThenDelete] = doc.getMapConflicts()
  assertConflictShape(deleteThenSet)
  assertConflictShape(setThenDelete)
  t.assert(deleteThenSet.type === 'delete-set' && setThenDelete.type === 'delete-set')
  t.compare(deleteThenSet.writes.map(write => write.snapshot.summary), ['delete "a" (was 1)', 'set "a" = 2'])
  t.assert(deleteThenSet.resolution.winner?.op === 'set')
  t.compare(setThenDelete.writes.map(write => write.op), ['set', 'delete'])
  t.assert(setThenDelete.resolution.winner?.op === 'delete')
}

/**
 * @param {t.TestCase} _tc
 */
export const testWritesThatDoNotConflict = _tc => {
  for (const policy of /** @type {Array<'collect'|'error'>} */ (['collect', 'error'])) {
    const doc = new Y.Doc({ mapConflictPolicy: policy })
    const map = doc.get('map')
    // separate transactions
    map.setAttr('a', 1)
    map.setAttr('a', 2)
    map.deleteAttr('a')
    map.setAttr('a', 3)
    const nested = map.setAttr('nested', new Y.Type())
    doc.transact(() => {
      // different keys
      map.setAttr('b', 1)
      map.setAttr('c', 1)
      // deleting a missing key doesn't write anything
      map.deleteAttr('missing')
      map.setAttr('missing', 1)
      // the same key of a different parent
      nested.setAttr('b', 1)
      doc.get('other').setAttr('b', 1)
    })
    t.compare(doc.getMapConflicts(), [])
  }
}

/**
 * @param {t.TestCase} _tc
 */
export const testLocalConflictsThrowInErrorMode = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  const map = doc.get('map')
  let observerCalls = 0
  map.observe(() => { observerCalls++ })
  const err = catchMapConflictError(() => doc.transact(() => {
    map.setAttr('title', 'draft')
    map.setAttr('title', 'final')
  }))
  t.assert(err instanceof Error && err.name === 'MapConflictError' && err.message.includes('"title"'))
  t.assert(Array.isArray(err.conflicts) && err.conflicts.length === 1)
  const [conflict] = err.conflicts
  assertConflictShape(conflict)
  t.assert(conflict.type === 'set-set' && conflict.source === 'local')
  t.assert(conflict.resolution.winner === conflict.writes[1])
  // the conflicting write was rejected before it was applied
  t.assert(map.getAttr('title') === 'draft' && observerCalls === 1)
  map.setAttr('b', 1)
  const deleteSet = catchMapConflictError(() => doc.transact(() => {
    map.deleteAttr('b')
    map.setAttr('b', 2)
  }))
  t.assert(deleteSet.conflicts[0].type === 'delete-set' && !map.hasAttr('b'))
  const setDelete = catchMapConflictError(() => doc.transact(() => {
    map.setAttr('c', 1)
    map.deleteAttr('c')
  }))
  t.assert(setDelete.conflicts[0].type === 'delete-set' && map.getAttr('c') === 1)
  doc.transact(() => {
    map.setAttr('d', 1)
    map.setAttr('e', 1)
  })
  t.assert(map.getAttr('d') === 1 && map.getAttr('e') === 1)
  t.compare(doc.getMapConflicts(), [])
}

/**
 * @param {t.TestCase} _tc
 */
export const testTypesAndSubdocsAreAmbiguous = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('map')
  map.setAttr('nested', new Y.Type())
  doc.transact(() => {
    map.setAttr('type', new Y.Type())
    map.setAttr('type', new Y.Type('paragraph'))
    map.setAttr('subdoc', new Y.Doc())
    map.setAttr('subdoc', 'plain value')
    map.deleteAttr('nested')
    map.setAttr('nested', 'replaced')
    map.setAttr('plain', 1)
    map.setAttr('plain', 2)
  })
  const byKey = Object.fromEntries(doc.getMapConflicts().map(conflict => [conflict.key, conflict]))
  for (const key of ['type', 'subdoc', 'nested']) {
    assertConflictShape(byKey[key])
    t.assert(byKey[key].type === 'ambiguous' && byKey[key].ambiguous)
    t.assert(byKey[key].message.includes('Ambiguous'))
  }
  t.assert(byKey.type.kind === 'set-set' && byKey.subdoc.kind === 'set-set' && byKey.nested.kind === 'delete-set')
  t.compare(byKey.type.writes.map(write => write.snapshot.summary), ['set "type" = Y.Type', 'set "type" = Y.Type<paragraph>'])
  t.assert(byKey.subdoc.writes[0].ambiguous && byKey.subdoc.writes[0].snapshot.valueType === 'doc')
  t.assert(!byKey.subdoc.writes[1].ambiguous)
  t.assert(byKey.nested.writes[0].op === 'delete' && byKey.nested.writes[0].snapshot.valueType === 'type')
  t.assert(byKey.plain.type === 'set-set' && !byKey.plain.ambiguous)
  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  const err = catchMapConflictError(() => strict.transact(() => {
    strict.get('map').setAttr('subdoc', new Y.Doc())
    strict.get('map').setAttr('subdoc', new Y.Doc())
  }))
  t.assert(err.conflicts[0].type === 'ambiguous' && err.conflicts[0].ambiguous)
  t.assert(strict.getSubdocs().size === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testMergedUpdateSetSet = _tc => {
  const merged = Y.mergeUpdates([
    createUpdate(1, map => map.setAttr('k', 'one')),
    createUpdate(2, map => map.setAttr('k', 'two'))
  ])
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(doc, merged)
  t.assert(doc.get('map').getAttr('k') === 'two')
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  const [conflict] = conflicts
  assertConflictShape(conflict)
  t.assert(conflict.type === 'set-set' && conflict.source === 'remote' && conflict.parentId === 'root:map')
  t.compare(conflict.writes.map(write => write.id), ['1:0', '2:0'])
  t.compare(conflict.writes.map(write => write.source), ['remote', 'remote'])
  // concurrent map writes are ordered by client id
  t.assert(conflict.resolution.winner === conflict.writes[1])
  t.assert(conflict.resolution.strategy === 'client-id-order')
}

/**
 * @param {t.TestCase} _tc
 */
export const testMergedUpdateDeleteSet = _tc => {
  const source = new Y.Doc()
  source.clientID = 1
  const updates = recordUpdates(source)
  source.get('map').setAttr('k', 'v')
  source.get('map').deleteAttr('k')
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(doc, Y.mergeUpdates(updates))
  t.assert(!doc.get('map').hasAttr('k'))
  const [conflict] = doc.getMapConflicts()
  assertConflictShape(conflict)
  t.assert(conflict.type === 'delete-set' && conflict.source === 'remote')
  t.compare(conflict.writes.map(write => write.snapshot.summary), ['set "k" = "v"', 'delete "k" (was "v")'])
  t.assert(conflict.resolution.winner === conflict.writes[1])
  t.assert(conflict.resolution.strategy === 'last-write-wins')
}

/**
 * The value of a key is deleted by one peer while another peer concurrently sets the key.
 *
 * @param {t.TestCase} _tc
 */
export const testMergedUpdateConcurrentDeleteSet = _tc => {
  const base = new Y.Doc()
  base.clientID = 100
  base.get('map').setAttr('k', 'base')
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(doc, Y.encodeStateAsUpdate(base))
  const sv = Y.encodeStateVector(doc)
  base.get('map').deleteAttr('k')
  Y.applyUpdate(doc, Y.mergeUpdates([
    Y.encodeStateAsUpdate(base, sv),
    createUpdate(1, map => map.setAttr('k', 'concurrent'))
  ]))
  t.assert(!doc.get('map').hasAttr('k'))
  const [conflict] = doc.getMapConflicts()
  assertConflictShape(conflict)
  t.assert(conflict.type === 'delete-set' && conflict.source === 'remote')
  t.assert(conflict.resolution.winner?.op === 'delete' && conflict.resolution.strategy === 'client-id-order')
}

/**
 * @type {Array<[string, function():Uint8Array<ArrayBuffer>]>}
 */
const conflictingUpdates = [
  ['set-set', () => Y.mergeUpdates([
    createUpdate(1, map => {
      map.setAttr('k', 'one')
      map.setAttr('a', 1)
    }),
    createUpdate(2, map => {
      map.setAttr('k', 'two')
      map.setAttr('b', 2)
    })
  ])],
  ['delete-set', () => {
    const source = new Y.Doc()
    source.clientID = 1
    const updates = recordUpdates(source)
    source.get('map').setAttr('a', 1)
    source.get('map').setAttr('k', 'v')
    source.get('map').deleteAttr('k')
    source.get('text').insert(0, 'abc')
    return Y.mergeUpdates(updates)
  }],
  ['ambiguous', () => Y.mergeUpdates([
    createUpdate(1, map => { map.setAttr('k', new Y.Type()).setAttr('x', 1) }),
    createUpdate(2, map => { map.setAttr('k', new Y.Type()).setAttr('y', 2) })
  ])],
  ['ambiguous', () => Y.mergeUpdates([
    createUpdate(1, map => { map.setAttr('k', new Y.Doc()) }),
    createUpdate(2, map => { map.setAttr('k', new Y.Doc()) })
  ])]
]

/**
 * @param {t.TestCase} _tc
 */
export const testConflictingUpdatesAreRejectedAtomically = _tc => {
  conflictingUpdates.forEach(([type, createConflictingUpdate]) => {
    for (const v2 of [false, true]) {
      const update = v2 ? Y.convertUpdateFormatV1ToV2(createConflictingUpdate()) : createConflictingUpdate()
      const apply = v2 ? Y.applyUpdateV2 : Y.applyUpdate
      const doc = new Y.Doc({ mapConflictPolicy: 'error' })
      doc.get('text').insert(0, 'hello')
      doc.get('map').setAttr('untouched', true)
      const stateBefore = Y.encodeStateAsUpdate(doc)
      let events = 0
      doc.on('update', () => { events++ })
      doc.get('map').observeDeep(() => { events++ })
      const err = catchMapConflictError(() => apply(doc, update))
      t.assert(err.conflicts.length === 1 && err.conflicts[0].type === type && err.conflicts[0].key === 'k')
      assertConflictShape(err.conflicts[0])
      t.assert(err.conflicts[0].source === 'remote')
      // nothing was applied
      t.compare(Y.encodeStateAsUpdate(doc), stateBefore)
      t.compare(doc.get('map').toJSON(), { attrs: { untouched: true } })
      t.assert(doc.get('text').toString() === 'hello')
      t.assert(events === 0 && doc.getSubdocs().size === 0)
      t.assert(doc.store.pendingStructs === null && doc.store.pendingDs === null)
      t.compare(doc.getMapConflicts(), [])
      // the same update applies normally without conflict detection
      const lenient = new Y.Doc()
      apply(lenient, update)
      t.assert(lenient.get('map').hasAttr('k') === (type !== 'delete-set'))
    }
  })
}

/**
 * @param {t.TestCase} _tc
 */
export const testMixedSource = _tc => {
  const remoteUpdate = createUpdate(99, map => map.setAttr('k', 'remote'))
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  doc.transact(() => {
    doc.get('map').setAttr('k', 'local')
    Y.applyUpdate(doc, remoteUpdate)
  })
  const [conflict] = doc.getMapConflicts()
  assertConflictShape(conflict)
  t.assert(conflict.source === 'mixed' && conflict.type === 'set-set')
  t.compare(conflict.writes.map(write => write.source), ['local', 'remote'])
  t.assert(conflict.resolution.winner?.snapshot.value === doc.get('map').getAttr('k'))
  // a remote update that conflicts with a local write of the same transaction is rejected
  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  const err = catchMapConflictError(() => strict.transact(() => {
    strict.get('map').setAttr('k', 'local')
    Y.applyUpdate(strict, remoteUpdate)
  }))
  t.assert(err.conflicts[0].source === 'mixed' && strict.get('map').getAttr('k') === 'local')
  // and so is a local write that conflicts with a remote write of the same transaction
  const strict2 = new Y.Doc({ mapConflictPolicy: 'error' })
  const err2 = catchMapConflictError(() => strict2.transact(() => {
    Y.applyUpdate(strict2, remoteUpdate)
    strict2.get('map').setAttr('k', 'local')
  }))
  t.assert(err2.conflicts[0].source === 'mixed' && strict2.get('map').getAttr('k') === 'remote')
}

/**
 * @param {t.TestCase} _tc
 */
export const testRegularSyncIsNotAConflict = _tc => {
  const source = new Y.Doc()
  const incremental = new Y.Doc({ mapConflictPolicy: 'error' })
  source.on('update', update => { Y.applyUpdate(incremental, update) })
  const map = source.get('map')
  map.setAttr('k', 1)
  map.setAttr('k', 2)
  map.deleteAttr('k')
  map.setAttr('k', 3)
  map.setAttr('nested', new Y.Type())
  map.setAttr('nested', new Y.Type())
  map.clearAttrs()
  map.setAttr('k', 4)
  // garbage collected history
  const full = new Y.Doc({ mapConflictPolicy: 'error' })
  Y.applyUpdate(full, Y.encodeStateAsUpdate(source))
  // concurrent writes that arrive in different updates
  const a = createUpdate(1, map => map.setAttr('c', 'a'))
  const b = createUpdate(2, map => map.setAttr('c', 'b'))
  Y.applyUpdate(incremental, a)
  Y.applyUpdate(incremental, b)
  Y.applyUpdate(full, b)
  Y.applyUpdate(full, a)
  // concurrent writes that were already resolved by a garbage collecting peer
  const relay = new Y.Doc()
  Y.applyUpdate(relay, a)
  Y.applyUpdate(relay, b)
  const relayed = new Y.Doc({ mapConflictPolicy: 'error' })
  Y.applyUpdate(relayed, Y.encodeStateAsUpdate(relay))
  t.compare(incremental.get('map').toJSON(), { attrs: { k: 4, c: 'b' } })
  t.compare(full.get('map').toJSON(), { attrs: { k: 4, c: 'b' } })
  t.compare(relayed.get('map').toJSON(), { attrs: { c: 'b' } })
}

/**
 * @param {t.TestCase} _tc
 */
export const testDeletedTypeDoesNotConflictWithItsKeys = _tc => {
  for (const remote of [false, true]) {
    const source = new Y.Doc({ mapConflictPolicy: remote ? 'allow' : 'collect' })
    const doc = remote ? new Y.Doc({ mapConflictPolicy: 'collect' }) : source
    source.on('update', update => { remote && Y.applyUpdate(doc, update) })
    const inner = source.get('map').setAttr('inner', new Y.Type())
    source.transact(() => {
      inner.setAttr('x', 1)
      source.get('map').deleteAttr('inner')
    })
    t.assert(!doc.get('map').hasAttr('inner'))
    t.compare(doc.getMapConflicts(), [])
  }
}

/**
 * @param {t.TestCase} _tc
 */
export const testParentIds = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const nested = doc.get('map').setAttr('nested', new Y.Type())
  doc.transact(() => {
    nested.setAttr('k', 1)
    nested.setAttr('k', 2)
    doc.get('other').setAttr('k', 1)
    doc.get('other').setAttr('k', 2)
    doc.get().setAttr('k', 1)
    doc.get().setAttr('k', 2)
  })
  const { id } = /** @type {Y.Item} */ (nested._item)
  t.compare(doc.getMapConflicts().map(conflict => conflict.parentId), [`${id.client}:${id.clock}`, 'root:other', 'root:'])
  // remote conflicts in nested types use the same parent id
  const remote = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(remote, Y.encodeStateAsUpdate(doc))
  const sv = Y.encodeStateVector(remote)
  const peer = new Y.Doc()
  peer.clientID = 7
  Y.applyUpdate(peer, Y.encodeStateAsUpdate(doc))
  const peerNested = /** @type {Y.Type} */ (peer.get('map').getAttr('nested'))
  peerNested.setAttr('k', 3)
  nested.setAttr('k', 4)
  Y.applyUpdate(remote, Y.mergeUpdates([Y.encodeStateAsUpdate(peer, sv), Y.encodeStateAsUpdate(doc, sv)]))
  const [conflict] = remote.getMapConflicts()
  t.assert(conflict.parentId === `${id.client}:${id.clock}` && conflict.source === 'remote')
}

/**
 * @param {t.TestCase} _tc
 */
export const testConflictSummary = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('map')
  map.setAttr('d', 0)
  doc.transact(() => {
    map.setAttr('a', 1)
    map.setAttr('a', 2)
    map.setAttr(/** @type {string} */ ('constructor'), 1)
    map.setAttr(/** @type {string} */ ('constructor'), 2)
    map.deleteAttr('d')
    map.setAttr('d', 1)
    map.setAttr('t', new Y.Type())
    map.setAttr('t', new Y.Type())
    doc.get('other').setAttr('a', 1)
    doc.get('other').setAttr('a', 2)
  })
  Y.applyUpdate(doc, Y.mergeUpdates([
    createUpdate(1, map => map.setAttr('a', 'x')),
    createUpdate(2, map => map.setAttr('a', 'y'))
  ]))
  const summary = doc.getMapConflictSummary()
  t.assert(summary.count === 6 && summary.total === 6)
  t.compare(summary.byType, { 'set-set': 4, 'delete-set': 1, ambiguous: 1 })
  t.compare(Object.entries(summary.byKey), [['a', 3], ['constructor', 1], ['d', 1], ['t', 1]])
  t.compare(summary.byParent, { 'root:map': 5, 'root:other': 1 })
  t.compare(summary.bySource, { local: 5, remote: 1 })
  for (const counts of [summary.byType, summary.byKey, summary.byParent, summary.bySource]) {
    t.assert(Object.getPrototypeOf(counts) === Object.prototype)
    t.assert(Object.values(counts).reduce((sum, count) => sum + count, 0) === summary.count)
  }
  doc.getMapConflicts().forEach(conflict => {
    t.assert(summary.byType[conflict.type] > 0 && summary.byKey[conflict.key] > 0)
    t.assert(summary.byParent[conflict.parentId] > 0 && summary.bySource[conflict.source] > 0)
  })
  doc.clearMapConflicts()
  t.compare(doc.getMapConflicts(), [])
  t.compare(doc.getMapConflictSummary(), { count: 0, total: 0, byType: {}, byKey: {}, byParent: {}, bySource: {} })
}

/**
 * @param {t.TestCase} _tc
 */
export const testDetectionIsDeterministic = _tc => {
  const updates = [3, 1, 2].map(client => createUpdate(client, map => {
    map.setAttr('k', client)
    map.setAttr('j', client)
  }))
  /**
   * @param {Y.Doc} doc
   */
  const describe = doc => doc.getMapConflicts().map(conflict => ({
    key: conflict.key,
    type: conflict.type,
    writes: conflict.writes.map(write => write.id),
    winner: conflict.resolution.winner?.id,
    strategy: conflict.resolution.strategy
  }))
  const results = [updates, updates.slice().reverse()].flatMap(order => {
    const v1 = new Y.Doc({ mapConflictPolicy: 'collect' })
    Y.applyUpdate(v1, Y.mergeUpdates(order))
    const v2 = new Y.Doc({ mapConflictPolicy: 'collect' })
    Y.applyUpdateV2(v2, Y.mergeUpdatesV2(order.map(update => Y.convertUpdateFormatV1ToV2(update))))
    return [describe(v1), describe(v2)]
  })
  results.forEach(result => t.compare(result, results[0]))
  t.compare(results[0], [
    { key: 'k', type: 'set-set', writes: ['1:0', '2:0', '3:0'], winner: '3:0', strategy: 'client-id-order' },
    { key: 'j', type: 'set-set', writes: ['1:1', '2:1', '3:1'], winner: '3:1', strategy: 'client-id-order' }
  ])
  // the error mode predicts the same resolution before the update is applied
  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  const err = catchMapConflictError(() => Y.applyUpdate(strict, Y.mergeUpdates(updates)))
  t.compare(err.conflicts.map(conflict => conflict.resolution.winner?.id), ['3:0', '3:1'])
  t.compare(err.conflicts.map(conflict => conflict.resolution.strategy), ['client-id-order', 'client-id-order'])
}

/**
 * Pending structs that are unblocked by an update are integrated in the same transaction.
 *
 * @param {t.TestCase} _tc
 */
export const testPendingStructs = _tc => {
  const source = new Y.Doc()
  source.clientID = 1
  const updates = recordUpdates(source)
  const nested = source.get('map').setAttr('nested', new Y.Type())
  nested.setAttr('x', 'pending')
  const peer = new Y.Doc()
  peer.clientID = 2
  Y.applyUpdate(peer, updates[0])
  const peerNested = /** @type {Y.Type} */ (peer.get('map').getAttr('nested'))
  peerNested.setAttr('x', 'concurrent')
  const unblocking = Y.mergeUpdates([updates[0], Y.encodeStateAsUpdate(peer)])
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(doc, updates[1])
  t.assert(doc.store.pendingStructs !== null)
  Y.applyUpdate(doc, unblocking)
  t.assert(doc.store.pendingStructs === null)
  const [conflict] = doc.getMapConflicts()
  assertConflictShape(conflict)
  t.assert(conflict.key === 'x' && conflict.type === 'set-set' && conflict.source === 'remote')
  t.compare(conflict.writes.map(write => write.snapshot.summary), ['set "x" = "pending"', 'set "x" = "concurrent"'])
  t.assert(conflict.resolution.winner === conflict.writes[1])
  t.compare(doc.get('map').toJSON(), { attrs: { nested: { attrs: { x: 'concurrent' } } } })
  // the update is rejected together with the pending structs it would unblock
  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  Y.applyUpdate(strict, updates[1])
  const pendingBefore = strict.store.pendingStructs
  const err = catchMapConflictError(() => Y.applyUpdate(strict, unblocking))
  t.assert(err.conflicts.length === 1 && err.conflicts[0].key === 'x')
  t.assert(strict.store.pendingStructs === pendingBefore)
  t.compare(strict.get('map').toJSON(), {})
  Y.applyUpdate(strict, updates[0])
  t.assert(strict.store.pendingStructs === null)
  t.compare(strict.get('map').toJSON(), { attrs: { nested: { attrs: { x: 'pending' } } } })
}
