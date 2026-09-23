import * as Y from '../src/index.js'
import * as t from 'lib0/testing'

/**
 * @param {'allow'|'collect'|'error'} [policy]
 */
const docWith = (policy = 'allow') => {
  const doc = new Y.Doc({ mapConflictPolicy: policy })
  const map = doc.get('map')
  return { doc, map }
}

/**
 * @param {t.TestCase} tc
 */
export const testAllowDoesNotCollectOrBlock = tc => {
  const { doc, map } = docWith('allow')
  doc.transact(() => {
    map.setAttr('a', 1)
    map.setAttr('a', 2)
    map.deleteAttr('a')
    map.setAttr('a', 3)
  })
  t.assert(map.getAttr('a') === 3)
  t.assert(doc.getMapConflicts().length === 0)
  t.assert(doc.getMapConflictSummary().count === 0)
  t.assert(doc.getMapConflictSummary().total === 0)
}

/**
 * @param {t.TestCase} tc
 */
export const testCollectSetSetInTransaction = tc => {
  const { doc, map } = docWith('collect')
  doc.transact(() => {
    map.setAttr('a', 1)
    map.setAttr('a', 2)
  })
  t.assert(map.getAttr('a') === 2)
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  const conflict = conflicts[0]
  t.assert(conflict.key === 'a')
  t.assert(conflict.parentId === 'map')
  t.assert(conflict.type === 'set-set')
  t.assert(conflict.ambiguous === false)
  t.assert(conflict.source === 'local')
  t.assert(typeof conflict.message === 'string' && conflict.message.length > 0)
  t.assert(conflict.writes.length >= 2)
  for (const write of conflict.writes) {
    t.assert(typeof write.snapshot.summary === 'string' && write.snapshot.summary.length > 0)
  }
  t.assert(typeof conflict.resolution.winner === 'string')
  t.assert(typeof conflict.resolution.strategy === 'string' && conflict.resolution.strategy.length > 0)
  t.assert(conflict.resolution.deterministic === true)
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType['set-set'] === 1)
  t.assert(summary.byKey.a === 1)
  t.assert(summary.byParent.map === 1)
  t.assert(summary.bySource.local === 1)
  t.assert(summary.count === 1)
  t.assert(summary.total === 1)
}

/**
 * @param {t.TestCase} tc
 */
export const testCollectDeleteSetInTransaction = tc => {
  const { doc, map } = docWith('collect')
  map.setAttr('a', 1)
  doc.transact(() => {
    map.deleteAttr('a')
    map.setAttr('a', 2)
  })
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  t.assert(conflicts[0].type === 'delete-set')
  t.assert(conflicts[0].key === 'a')
  t.assert(conflicts[0].source === 'local')
  t.assert(conflicts[0].writes.some(write => write.snapshot.summary.length > 0))
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType['delete-set'] === 1)
  t.assert(summary.count === 1)
}

/**
 * @param {t.TestCase} tc
 */
export const testAmbiguousTypeAndSubdoc = tc => {
  const { doc, map } = docWith('collect')
  doc.transact(() => {
    map.setAttr('nested', new Y.Type())
    map.setAttr('nested', new Y.Type())
  })
  doc.transact(() => {
    map.setAttr('sub', new Y.Doc())
    map.setAttr('sub', new Y.Doc())
  })
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 2)
  for (const conflict of conflicts) {
    t.assert(conflict.type === 'ambiguous' || conflict.ambiguous === true)
    t.assert(conflict.ambiguous === true)
    t.assert(conflict.writes.every(write => write.snapshot.summary.length > 0))
  }
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType.ambiguous === 2)
  t.assert(summary.total === 2)
}

/**
 * @param {t.TestCase} tc
 */
export const testErrorRollsBackLocalTransaction = tc => {
  const { doc, map } = docWith('error')
  map.setAttr('keep', 'yes')
  let caught = /** @type {any} */ (null)
  try {
    doc.transact(() => {
      map.setAttr('keep', 'no')
      map.setAttr('other', 1)
      map.setAttr('keep', 'still-no')
    })
  } catch (e) {
    caught = e
  }
  t.assert(caught instanceof Y.MapConflictError)
  t.assert(Array.isArray(caught.conflicts) && caught.conflicts.length > 0)
  t.assert(map.getAttr('keep') === 'yes')
  t.assert(map.getAttr('other') === undefined)
  t.assert(doc.getMapConflicts().length === 0)
}

/**
 * @param {t.TestCase} tc
 */
export const testMergedSetSetIsAtomic = tc => {
  const left = new Y.Doc()
  const right = new Y.Doc()
  left.clientID = 1
  right.clientID = 2
  left.get('map').setAttr('a', 1)
  left.get('map').setAttr('extra', 'from-left')
  right.get('map').setAttr('a', 2)
  right.get('map').setAttr('other', 'from-right')
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(left), Y.encodeStateAsUpdate(right)])

  const collect = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(collect, merged)
  const conflicts = collect.getMapConflicts()
  t.assert(conflicts.some(conflict => conflict.key === 'a' && (conflict.type === 'set-set' || conflict.ambiguous)))
  t.assert(conflicts.every(conflict => conflict.source === 'remote' || conflict.source === 'mixed' || conflict.source === 'local'))
  const remote = conflicts.find(conflict => conflict.key === 'a')
  if (remote == null) {
    throw new Error('expected a conflict on key a')
  }
  t.assert(remote.source === 'remote')
  t.assert(remote.writes.every(write => write.snapshot.summary.length > 0))
  t.assert(typeof remote.resolution.strategy === 'string')
  t.assert(typeof remote.resolution.deterministic === 'boolean')
  const summary = collect.getMapConflictSummary()
  t.assert(summary.byType[remote.type] >= 1)
  t.assert(summary.byKey.a >= 1)
  t.assert(summary.byParent.map >= 1)
  t.assert(summary.bySource[remote.source] >= 1)
  t.assert(summary.count === conflicts.length)

  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  strict.get('map').setAttr('untouched', 1)
  const before = Y.encodeStateAsUpdate(strict)
  let err = /** @type {any} */ (null)
  try {
    Y.applyUpdate(strict, merged)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.conflicts.length > 0)
  t.assert(strict.get('map').getAttr('untouched') === 1)
  t.assert(strict.get('map').getAttr('a') === undefined)
  t.assert(strict.get('map').getAttr('extra') === undefined)
  t.assert(strict.get('map').getAttr('other') === undefined)
  t.compare(Y.encodeStateAsUpdate(strict), before)
}

/**
 * @param {t.TestCase} tc
 */
export const testMergedDeleteSet = tc => {
  const base = new Y.Doc()
  base.clientID = 1
  base.get('map').setAttr('a', 'base')
  const state = Y.encodeStateAsUpdate(base)
  const deleter = new Y.Doc()
  const setter = new Y.Doc()
  deleter.clientID = 2
  setter.clientID = 3
  Y.applyUpdate(deleter, state)
  Y.applyUpdate(setter, state)
  deleter.get('map').deleteAttr('a')
  setter.get('map').setAttr('a', 'next')
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(deleter), Y.encodeStateAsUpdate(setter)])
  const collect = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(collect, merged)
  const conflicts = collect.getMapConflicts()
  t.assert(conflicts.some(conflict => conflict.key === 'a' && (conflict.type === 'delete-set' || conflict.type === 'ambiguous' || conflict.ambiguous)))
  const strict = new Y.Doc({ mapConflictPolicy: 'error' })
  strict.get('map').setAttr('safe', 1)
  let err = /** @type {any} */ (null)
  try {
    Y.applyUpdate(strict, merged)
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(Array.isArray(err.conflicts))
  t.assert(strict.get('map').getAttr('safe') === 1)
  t.assert(strict.get('map').getAttr('a') === undefined)
}

/**
 * @param {t.TestCase} tc
 */
export const testSequentialWritesAreNotConflicts = tc => {
  const { doc, map } = docWith('collect')
  map.setAttr('a', 1)
  map.setAttr('a', 2)
  map.deleteAttr('b')
  t.assert(doc.getMapConflicts().length === 0)
  t.assert(map.getAttr('a') === 2)
}
