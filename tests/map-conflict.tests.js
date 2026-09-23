import * as Y from '../src/index.js'
import * as t from 'lib0/testing'

/**
 * @param {any} conflict
 */
const assertConflictShape = conflict => {
  t.assert(typeof conflict.key === 'string')
  t.assert(typeof conflict.parentId === 'string')
  t.assert(conflict.type === 'set-set' || conflict.type === 'delete-set' || conflict.type === 'ambiguous')
  t.assert(conflict.ambiguous === true || conflict.ambiguous === false)
  t.assert(conflict.source === 'local' || conflict.source === 'remote' || conflict.source === 'mixed')
  t.assert(typeof conflict.message === 'string' && conflict.message.length > 0)
  t.assert(Array.isArray(conflict.writes) && conflict.writes.length >= 2)
  for (let i = 0; i < conflict.writes.length; i++) {
    const summary = conflict.writes[i].snapshot.summary
    t.assert(typeof summary === 'string' && summary.length > 0)
  }
  t.assert(typeof conflict.resolution.winner === 'string' && conflict.resolution.winner.length > 0)
  t.assert(typeof conflict.resolution.strategy === 'string' && conflict.resolution.strategy.length > 0)
  t.assert(conflict.resolution.deterministic === true)
}

/**
 * @param {Y.Doc} doc
 * @param {string} type
 */
const assertSummary = (doc, type) => {
  const summary = doc.getMapConflictSummary()
  t.assert(summary.count === doc.getMapConflicts().length)
  t.assert(summary.total === summary.count)
  t.assert(summary.byType[type] >= 1)
  t.assert(typeof summary.byKey === 'object' && summary.byKey != null)
  t.assert(typeof summary.byParent === 'object' && summary.byParent != null)
  t.assert(typeof summary.bySource === 'object' && summary.bySource != null)
  return summary
}

/**
 * @param {t.TestCase} _tc
 */
export const testAllowPolicyAppliesWithoutCollecting = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'allow' })
  const map = doc.get('m')
  doc.transact(() => {
    map.setAttr('k', 1)
    map.setAttr('k', 2)
    map.deleteAttr('k')
    map.setAttr('k', 3)
  })
  t.assert(map.getAttr('k') === 3)
  t.assert(doc.getMapConflicts().length === 0)
  t.assert(doc.getMapConflictSummary().count === 0)
  t.assert(doc.getMapConflictSummary().total === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testSequentialWritesAreNotConflicts = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  const map = doc.get('m')
  map.setAttr('k', 1)
  map.setAttr('k', 2)
  map.deleteAttr('k')
  map.setAttr('k', 3)
  t.assert(map.getAttr('k') === 3)
  t.assert(doc.getMapConflicts().length === 0)
  const replica = new Y.Doc({ mapConflictPolicy: 'error' })
  Y.applyUpdate(replica, Y.encodeStateAsUpdate(doc))
  t.assert(replica.get('m').getAttr('k') === 3)
  t.assert(replica.getMapConflicts().length === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectLocalSetSet = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('m')
  doc.transact(() => {
    map.setAttr('k', 'a')
    map.setAttr('k', 'b')
  })
  t.assert(map.getAttr('k') === 'b')
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  assertConflictShape(conflicts[0])
  t.assert(conflicts[0].type === 'set-set')
  t.assert(conflicts[0].ambiguous === false)
  t.assert(conflicts[0].source === 'local')
  t.assert(conflicts[0].key === 'k')
  t.assert(conflicts[0].parentId === 'm')
  const summary = assertSummary(doc, 'set-set')
  t.assert(summary.byType['set-set'] === 1)
  t.assert(summary.byKey.k === 1)
  t.assert(summary.byParent.m === 1)
  t.assert(summary.bySource.local === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectLocalDeleteSet = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('m')
  map.setAttr('keep', 1)
  doc.transact(() => {
    map.setAttr('k', 1)
    map.deleteAttr('k')
  })
  t.assert(map.getAttr('k') === undefined)
  t.assert(map.getAttr('keep') === 1)
  const conflict = doc.getMapConflicts()[0]
  assertConflictShape(conflict)
  t.assert(conflict.type === 'delete-set')
  t.assert(conflict.source === 'local')
  t.assert(conflict.key === 'k')
  const summary = assertSummary(doc, 'delete-set')
  t.assert(summary.byKey.k === 1)
  t.assert(summary.bySource.local === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testErrorLocalConflictsAreAtomic = _tc => {
  /**
   * @param {'set-set' | 'delete-set'} kind
   */
  const run = kind => {
    const doc = new Y.Doc({ mapConflictPolicy: 'error' })
    const map = doc.get('m')
    map.setAttr('keep', 'yes')
    map.setAttr('k', 'old')
    const before = Y.encodeStateAsUpdate(doc)
    /** @type {any} */
    let err = null
    try {
      doc.transact(() => {
        map.setAttr('other', 1)
        if (kind === 'set-set') {
          map.setAttr('k', 1)
          map.setAttr('k', 2)
        } else {
          map.deleteAttr('k')
          map.setAttr('k', 2)
        }
      })
    } catch (e) {
      err = e
    }
    t.assert(err instanceof Y.MapConflictError)
    t.assert(Array.isArray(err.conflicts) && err.conflicts.length === 1)
    assertConflictShape(err.conflicts[0])
    t.assert(err.conflicts[0].type === kind)
    t.assert(err.conflicts[0].source === 'local')
    t.compare(Y.encodeStateAsUpdate(doc), before)
    t.assert(map.getAttr('keep') === 'yes')
    t.assert(map.getAttr('k') === 'old')
    t.assert(map.getAttr('other') === undefined)
    t.assert(doc.getMapConflicts().length === 0)
    map.setAttr('after', true)
    t.assert(map.getAttr('after') === true)
  }
  run('set-set')
  run('delete-set')
}

/**
 * @param {t.TestCase} _tc
 */
export const testAmbiguousTypeAndSubdocConflicts = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('m')
  doc.transact(() => {
    map.setAttr('typed', new Y.Type())
    map.setAttr('typed', new Y.Type())
  })
  doc.transact(() => {
    map.setAttr('sub', new Y.Doc())
    map.setAttr('sub', new Y.Doc())
  })
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 2)
  for (let i = 0; i < conflicts.length; i++) {
    assertConflictShape(conflicts[i])
    t.assert(conflicts[i].type === 'ambiguous' || conflicts[i].ambiguous === true)
    t.assert(conflicts[i].ambiguous === true)
    t.assert(conflicts[i].source === 'local')
  }
  const summary = doc.getMapConflictSummary()
  t.assert(summary.count === 2)
  t.assert(summary.byType.ambiguous === 2)
  t.assert(summary.byParent.m === 2)
}

/**
 * @param {t.TestCase} _tc
 */
export const testMergedSetSetRemoteAndAtomic = _tc => {
  const left = new Y.Doc()
  left.clientID = 1
  left.get('m').setAttr('k', 'left')
  const right = new Y.Doc()
  right.clientID = 2
  right.get('m').setAttr('k', 'right')
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(left), Y.encodeStateAsUpdate(right)])

  const collecting = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(collecting, merged)
  t.assert(collecting.get('m').getAttr('k') === 'right')
  const conflict = collecting.getMapConflicts()[0]
  assertConflictShape(conflict)
  t.assert(conflict.type === 'set-set')
  t.assert(conflict.source === 'remote')
  t.assert(conflict.key === 'k')
  t.assert(conflict.parentId === 'm')
  const summary = assertSummary(collecting, 'set-set')
  t.assert(summary.bySource.remote === 1)
  t.assert(summary.byKey.k === 1)
  t.assert(summary.byParent.m === 1)

  const rejecting = new Y.Doc({ mapConflictPolicy: 'error' })
  rejecting.get('m').setAttr('keep', 1)
  const before = Y.encodeStateAsUpdate(rejecting)
  /** @type {any} */
  let err = null
  try {
    Y.applyUpdate(rejecting, merged)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(Array.isArray(err.conflicts) && err.conflicts.length >= 1)
  assertConflictShape(err.conflicts[0])
  t.assert(err.conflicts[0].type === 'set-set')
  t.compare(Y.encodeStateAsUpdate(rejecting), before)
  t.assert(rejecting.get('m').getAttr('keep') === 1)
  t.assert(rejecting.get('m').getAttr('k') === undefined)

  const mixed = new Y.Doc({ mapConflictPolicy: 'collect' })
  mixed.clientID = 1
  mixed.get('m').setAttr('k', 'left')
  Y.applyUpdate(mixed, Y.encodeStateAsUpdate(right))
  t.assert(mixed.getMapConflicts()[0].source === 'mixed')
  t.assert(mixed.getMapConflicts()[0].type === 'set-set')
  t.assert(mixed.get('m').getAttr('k') === 'right')
}

/**
 * @param {t.TestCase} _tc
 */
export const testDeleteSetAgainstRemoteSetIsAtomic = _tc => {
  const base = new Y.Doc()
  base.clientID = 1
  base.get('m').setAttr('k', 'v0')
  base.get('m').setAttr('keep', 'yes')
  const state = Y.encodeStateAsUpdate(base)
  const stateVector = Y.encodeStateVector(base)

  const deleter = new Y.Doc()
  deleter.clientID = 2
  Y.applyUpdate(deleter, state)
  deleter.get('m').deleteAttr('k')
  const deleteUpdate = Y.encodeStateAsUpdate(deleter, stateVector)

  const setter = new Y.Doc()
  setter.clientID = 3
  Y.applyUpdate(setter, state)
  setter.get('m').setAttr('k', 'v1')
  const setUpdate = Y.encodeStateAsUpdate(setter, stateVector)

  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  Y.applyUpdate(doc, state)
  Y.applyUpdate(doc, deleteUpdate)
  t.assert(doc.get('m').getAttr('k') === undefined)
  const before = Y.encodeStateAsUpdate(doc)
  /** @type {any} */
  let err = null
  try {
    Y.applyUpdate(doc, setUpdate)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.conflicts[0].type === 'delete-set' || err.conflicts[0].type === 'ambiguous')
  t.assert(err.conflicts[0].type === 'delete-set')
  assertConflictShape(err.conflicts[0])
  t.compare(Y.encodeStateAsUpdate(doc), before)
  t.assert(doc.get('m').getAttr('keep') === 'yes')
  t.assert(doc.get('m').getAttr('k') === undefined)

  const collecting = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(collecting, state)
  Y.applyUpdate(collecting, deleteUpdate)
  Y.applyUpdate(collecting, setUpdate)
  const conflict = collecting.getMapConflicts()[0]
  assertConflictShape(conflict)
  t.assert(conflict.type === 'delete-set')
  t.assert(conflict.source === 'mixed')
  t.assert(collecting.get('m').getAttr('k') === 'v1')
  const summary = assertSummary(collecting, 'delete-set')
  t.assert(summary.bySource.mixed === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testAmbiguousMergedTypeConflict = _tc => {
  const left = new Y.Doc()
  left.clientID = 1
  left.get('m').setAttr('k', new Y.Type())
  const right = new Y.Doc()
  right.clientID = 2
  right.get('m').setAttr('k', new Y.Doc())
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(left), Y.encodeStateAsUpdate(right)])
  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  doc.get('m').setAttr('keep', 1)
  const before = Y.encodeStateAsUpdate(doc)
  /** @type {any} */
  let err = null
  try {
    Y.applyUpdate(doc, merged)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.conflicts[0].type === 'ambiguous' || err.conflicts[0].ambiguous === true)
  t.compare(Y.encodeStateAsUpdate(doc), before)
  t.assert(doc.get('m').getAttr('keep') === 1)
  t.assert(doc.get('m').getAttr('k') === undefined)
}

/**
 * @param {t.TestCase} _tc
 */
export const testSummaryBucketsSeveralConflicts = _tc => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = doc.get('m')
  const nested = map.setAttr('nested', new Y.Type())
  doc.transact(() => {
    map.setAttr('a', 1)
    map.setAttr('a', 2)
    nested.setAttr('b', 1)
    nested.deleteAttr('b')
  })
  const summary = doc.getMapConflictSummary()
  t.assert(summary.count === 2)
  t.assert(summary.total === 2)
  t.assert(summary.byType['set-set'] === 1)
  t.assert(summary.byType['delete-set'] === 1)
  t.assert(summary.byKey.a === 1)
  t.assert(summary.byKey.b === 1)
  t.assert(summary.bySource.local === 2)
  t.assert(summary.byParent.m === 1)
  const nestedId = /** @type {Y.Item} */ (nested._item).id
  const nestedParent = nestedId.client + ':' + nestedId.clock
  t.assert(summary.byParent[nestedParent] === 1)
}
