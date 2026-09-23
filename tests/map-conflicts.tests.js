import * as Y from '../src/index.js'
import * as t from 'lib0/testing'

/**
 * @param {any} conflict
 */
const assertConflictShape = conflict => {
  t.assert(typeof conflict.key === 'string')
  t.assert(typeof conflict.parentId === 'string')
  t.assert(['set-set', 'delete-set', 'ambiguous'].includes(conflict.type))
  t.assert(['local', 'remote', 'mixed'].includes(conflict.source))
  t.assert(typeof conflict.message === 'string' && conflict.message.length > 0)
  t.assert(conflict.writes.length >= 2)
  conflict.writes.forEach(/** @param {any} w */ w => {
    t.assert(typeof w.snapshot.summary === 'string' && w.snapshot.summary.length > 0)
  })
  t.assert(conflict.resolution.winner != null)
  t.assert(typeof conflict.resolution.strategy === 'string')
  t.assert(typeof conflict.resolution.deterministic === 'boolean')
}

/**
 * @param {t.TestCase} _tc
 */
export const testInvalidPolicy = _tc => {
  t.fails(() => {
    // @ts-ignore
    new Y.Doc({ mapConflictPolicy: 'nope' }) // eslint-disable-line no-new
  })
  t.assert(new Y.Doc().mapConflictPolicy === 'allow')
}

/**
 * @param {t.TestCase} _tc
 */
export const testAllowPolicy = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'allow' })
  const ymap = ydoc.get('map')
  ydoc.transact(() => {
    ymap.setAttr('a', 1)
    ymap.setAttr('a', 2)
  })
  t.assert(ymap.getAttr('a') === 2)
  t.assert(ydoc.getMapConflicts().length === 0)
  t.assert(ydoc.getMapConflictSummary().count === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectLocal = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const ymap = ydoc.get('map')
  ymap.setAttr('existing', 0)
  ymap.setAttr('existing', 1) // separate transactions don't conflict
  t.assert(ydoc.getMapConflicts().length === 0)
  ydoc.transact(() => {
    ymap.setAttr('a', 1)
    ymap.setAttr('a', 2)
    ymap.setAttr('b', 1)
    ymap.deleteAttr('b')
    ymap.deleteAttr('existing')
    ymap.setAttr('existing', 'x')
  })
  t.assert(ymap.getAttr('a') === 2)
  t.assert(ymap.getAttr('b') === undefined)
  t.assert(ymap.getAttr('existing') === 'x')
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 3)
  conflicts.forEach(assertConflictShape)
  const [a, b, existing] = conflicts
  t.assert(a.type === 'set-set' && a.key === 'a' && a.parentId === 'map' && a.source === 'local')
  t.assert(a.resolution.winner.snapshot.summary === 'set "a" = 2')
  t.assert(b.type === 'delete-set' && b.resolution.winner.op === 'delete')
  t.assert(existing.type === 'delete-set' && existing.resolution.winner.op === 'set')
  const summary = ydoc.getMapConflictSummary()
  t.assert(summary.count === 3 && summary.total === 3)
  t.assert(summary.byType['set-set'] === 1)
  t.assert(summary.byType['delete-set'] === 2)
  t.assert(summary.byKey.a === 1)
  t.assert(summary.byParent.map === 3)
  t.assert(summary.bySource.local === 3)
  ydoc.clearMapConflicts()
  t.assert(ydoc.getMapConflicts().length === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testErrorLocal = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'error' })
  const ymap = ydoc.get('map')
  ymap.setAttr('a', 0)
  let err = /** @type {any} */ (null)
  try {
    ydoc.transact(() => {
      ymap.setAttr('a', 1)
      ymap.setAttr('a', 2)
    })
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.name === 'MapConflictError')
  t.assert(err.conflicts.length === 1)
  assertConflictShape(err.conflicts[0])
  t.assert(ymap.getAttr('a') === 1) // the conflicting write was blocked
  t.fails(() => {
    ydoc.transact(() => {
      ymap.setAttr('b', 1)
      ymap.deleteAttr('b')
    })
  })
  t.assert(ydoc.getMapConflicts().length === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testAmbiguousLocal = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const ymap = ydoc.get('map')
  ydoc.transact(() => {
    ymap.setAttr('t', new Y.Type())
    ymap.setAttr('t', 1)
    ymap.setAttr('d', new Y.Doc())
    ymap.setAttr('d', new Y.Doc())
  })
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 2)
  conflicts.forEach(c => {
    assertConflictShape(c)
    t.assert(c.type === 'ambiguous' && c.ambiguous === true)
  })
  t.assert(conflicts[0].kind === 'set-set')
  t.assert(ydoc.getMapConflictSummary().byType.ambiguous === 2)
}

/**
 * @param {number} client
 * @param {function(Y.Type):void} f
 */
const updateFrom = (client, f) => {
  const ydoc = new Y.Doc()
  ydoc.clientID = client
  f(ydoc.get('map'))
  return Y.encodeStateAsUpdate(ydoc)
}

/**
 * @param {t.TestCase} _tc
 */
export const testMergedUpdateSetSet = _tc => {
  const merged = Y.mergeUpdates([
    updateFrom(1, m => { m.setAttr('k', 'one'); m.setAttr('other', 1) }),
    updateFrom(2, m => m.setAttr('k', 'two'))
  ])
  const collecting = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(collecting, merged)
  t.assert(collecting.get('map').getAttr('k') === 'two')
  const conflicts = collecting.getMapConflicts()
  t.assert(conflicts.length === 1)
  assertConflictShape(conflicts[0])
  t.assert(conflicts[0].type === 'set-set' && conflicts[0].source === 'remote')
  t.assert(conflicts[0].resolution.winner.client === 2)
  t.assert(conflicts[0].resolution.strategy === 'highest-client-id-wins')
  // applying the same update again is not a conflict
  Y.applyUpdate(collecting, merged)
  t.assert(collecting.getMapConflicts().length === 1)

  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  let err = /** @type {any} */ (null)
  try {
    Y.applyUpdate(strict, merged)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.conflicts.length === 1)
  // nothing of the update was applied
  t.assert(strict.get('map').getAttr('other') === undefined)
  t.assert(strict.store.clients.size === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testMergedUpdateDeleteSet = _tc => {
  const source = new Y.Doc()
  const m = source.get('map')
  m.setAttr('keep', true)
  const u1 = Y.encodeStateAsUpdate(source)
  const sv = Y.encodeStateVector(source)
  source.transact(() => {
    m.setAttr('k', 1)
    m.deleteAttr('k')
    m.setAttr('other', 1)
  })
  const merged = Y.mergeUpdates([u1, Y.encodeStateAsUpdate(source, sv)])

  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  let err = /** @type {any} */ (null)
  try {
    Y.applyUpdate(strict, merged)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.conflicts.length === 1)
  t.assert(err.conflicts[0].type === 'delete-set' && err.conflicts[0].key === 'k')
  t.assert(err.conflicts[0].resolution.winner.op === 'delete')
  t.assert(strict.get('map').getAttr('keep') === undefined)
  t.assert(strict.get('map').getAttr('other') === undefined)

  const collecting = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(collecting, merged)
  t.assert(collecting.getMapConflicts().length === 1)
  t.assert(collecting.get('map').getAttr('other') === 1)
  t.assert(!collecting.get('map').hasAttr('k'))
}

/**
 * @param {t.TestCase} _tc
 */
export const testRemoteOverwriteIsNotAConflict = _tc => {
  const a = new Y.Doc()
  const b = new Y.Doc({ mapConflictPolicy: 'error' })
  a.get('map').setAttr('k', 1)
  Y.applyUpdate(b, Y.encodeStateAsUpdate(a))
  let sv = Y.encodeStateVector(a)
  a.get('map').setAttr('k', 2)
  Y.applyUpdate(b, Y.encodeStateAsUpdate(a, sv))
  t.assert(b.get('map').getAttr('k') === 2)
  sv = Y.encodeStateVector(a)
  a.get('map').deleteAttr('k')
  Y.applyUpdate(b, Y.encodeStateAsUpdate(a, sv))
  t.assert(!b.get('map').hasAttr('k'))
  // several writes to the same key within one update are reported, even if they were sequential
  sv = Y.encodeStateVector(a)
  a.get('map').setAttr('k', 3)
  a.get('map').setAttr('k', 4)
  t.fails(() => Y.applyUpdate(b, Y.encodeStateAsUpdate(a, sv)))
  t.assert(!b.get('map').hasAttr('k'))
}

/**
 * @param {t.TestCase} _tc
 */
export const testMixedSource = _tc => {
  const update = updateFrom(9, m => m.setAttr('k', 'remote'))
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  ydoc.clientID = 1
  ydoc.transact(() => {
    ydoc.get('map').setAttr('k', 'local')
    Y.applyUpdate(ydoc, update)
  })
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 1)
  t.assert(conflicts[0].source === 'mixed')
  t.assert(ydoc.getMapConflictSummary().bySource.mixed === 1)

  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  t.fails(() => {
    strict.transact(() => {
      strict.get('map').setAttr('k', 'local')
      Y.applyUpdate(strict, update)
    })
  })
  t.assert(strict.get('map').getAttr('k') === 'local')
}

/**
 * @param {t.TestCase} _tc
 */
export const testAmbiguousRemote = _tc => {
  const merged = Y.mergeUpdates([
    updateFrom(1, m => m.setAttr('k', new Y.Type())),
    updateFrom(2, m => m.setAttr('k', new Y.Doc()))
  ])
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(ydoc, merged)
  const [conflict] = ydoc.getMapConflicts()
  assertConflictShape(conflict)
  t.assert(conflict.type === 'ambiguous' && conflict.ambiguous)
  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  t.fails(() => Y.applyUpdate(strict, merged))
  t.assert(strict.store.clients.size === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testNestedParent = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const nested = ydoc.get('map').setAttr('nested', new Y.Type())
  ydoc.transact(() => {
    nested.setAttr('x', 1)
    nested.setAttr('x', 2)
  })
  const [conflict] = ydoc.getMapConflicts()
  const item = /** @type {Y.Item} */ (nested._item)
  t.assert(conflict.parentId === `${item.id.client}:${item.id.clock}`)
}
