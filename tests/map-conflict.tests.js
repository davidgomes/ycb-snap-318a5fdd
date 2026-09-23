import * as Y from '../src/index.js'
import * as t from 'lib0/testing'

/**
 * @param {Y.Doc} doc
 */
const root = doc => doc.get('m')

export const testAllowDoesNotCollectOrBlock = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'allow' })
  const map = root(doc)
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

  const other = new Y.Doc()
  other.get('m').setAttr('k', 'remote')
  Y.applyUpdate(doc, Y.encodeStateAsUpdate(other))
  t.assert(doc.getMapConflicts().length === 0)
  t.assert(map.getAttr('k') != null)
}

export const testCollectSetSetInTransaction = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = root(doc)
  doc.transact(() => {
    map.setAttr('k', 'a')
    map.setAttr('other', 1)
    map.setAttr('k', 'b')
  })
  t.assert(map.getAttr('k') === 'b')
  t.assert(map.getAttr('other') === 1)
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  const conflict = conflicts[0]
  t.assert(conflict.key === 'k')
  t.assert(conflict.parentId === 'm')
  t.assert(conflict.type === 'set-set')
  t.assert(conflict.ambiguous === false)
  t.assert(conflict.source === 'local')
  t.assert(typeof conflict.message === 'string' && conflict.message.length > 0)
  t.assert(conflict.writes.length === 2)
  for (let i = 0; i < conflict.writes.length; i++) {
    t.assert(typeof conflict.writes[i].snapshot.summary === 'string')
    t.assert(conflict.writes[i].snapshot.summary.length > 0)
  }
  t.assert(typeof conflict.resolution.strategy === 'string')
  t.assert(conflict.resolution.deterministic === true)
  t.assert(conflict.resolution.winner === 'b')
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType[conflict.type] === 1)
  t.assert(summary.byKey[conflict.key] === 1)
  t.assert(summary.byParent[conflict.parentId] === 1)
  t.assert(summary.bySource[conflict.source] === 1)
  t.assert(summary.count === 1)
  t.assert(summary.total === 1)
}

export const testCollectDeleteSetInTransaction = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = root(doc)
  map.setAttr('k', 'base')
  doc.transact(() => {
    map.deleteAttr('k')
    map.setAttr('k', 'next')
  })
  t.assert(map.getAttr('k') === 'next')
  const conflict = doc.getMapConflicts()[0]
  t.assert(conflict.type === 'delete-set')
  t.assert(conflict.source === 'local')
  t.assert(conflict.key === 'k')
  t.assert(conflict.writes.length >= 2)
  t.assert(conflict.resolution.winner === 'next')
  t.assert(conflict.resolution.deterministic === true)

  doc.transact(() => {
    map.setAttr('k', 'again')
    map.deleteAttr('k')
  })
  const second = doc.getMapConflicts()[1]
  t.assert(second.type === 'delete-set')
  t.assert(second.resolution.winner == null)
  t.assert(map.getAttr('k') === undefined)
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType['delete-set'] === 2)
  t.assert(summary.count === 2)
}

export const testAmbiguousTypeAndSubdoc = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = root(doc)
  doc.transact(() => {
    map.setAttr('t', new Y.Type())
    map.setAttr('t', new Y.Type())
  })
  doc.transact(() => {
    map.setAttr('s', new Y.Doc())
    map.deleteAttr('s')
  })
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 2)
  for (let i = 0; i < conflicts.length; i++) {
    t.assert(conflicts[i].type === 'ambiguous' || conflicts[i].ambiguous === true)
    t.assert(conflicts[i].ambiguous === true)
    t.assert(conflicts[i].resolution.deterministic === false)
    t.assert(conflicts[i].writes.every(write => write.snapshot.summary.length > 0))
  }
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType[conflicts[0].type] >= 1)
  t.assert(summary.byKey.t === 1)
  t.assert(summary.byKey.s === 1)
}

export const testRemoteMergedSetSet = () => {
  const a = new Y.Doc()
  const b = new Y.Doc()
  a.get('m').setAttr('k', 'from-a')
  a.get('m').setAttr('only-a', 1)
  b.get('m').setAttr('k', 'from-b')
  b.get('m').setAttr('only-b', 2)
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(a), Y.encodeStateAsUpdate(b)])
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  doc.get('m').setAttr('keep', true)
  Y.applyUpdate(doc, merged)
  const conflicts = doc.getMapConflicts()
  t.assert(conflicts.length === 1)
  const conflict = conflicts[0]
  t.assert(conflict.key === 'k')
  t.assert(conflict.type === 'set-set')
  t.assert(conflict.source === 'remote')
  t.assert(conflict.parentId === 'm')
  t.assert(conflict.message.length > 0)
  t.assert(conflict.writes.length === 2)
  t.assert(conflict.writes.every(write => write.snapshot.summary.length > 0))
  t.assert(typeof conflict.resolution.strategy === 'string')
  t.assert(typeof conflict.resolution.deterministic === 'boolean')
  t.assert(doc.get('m').getAttr('keep') === true)
  t.assert(doc.get('m').getAttr('only-a') === 1)
  t.assert(doc.get('m').getAttr('only-b') === 2)
  const winner = doc.get('m').getAttr('k')
  t.assert(winner === 'from-a' || winner === 'from-b')
  t.assert(conflict.resolution.winner === winner)
  const summary = doc.getMapConflictSummary()
  t.assert(summary.byType['set-set'] === 1)
  t.assert(summary.bySource.remote === 1)
  t.assert(summary.byKey.k === 1)
  t.assert(summary.byParent.m === 1)
  t.assert(summary.count === conflicts.length)
}

export const testRemoteMergedDeleteSet = () => {
  const a = new Y.Doc()
  const b = new Y.Doc()
  a.get('m').setAttr('k', 'gone')
  a.get('m').deleteAttr('k')
  b.get('m').setAttr('k', 'stays')
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(a), Y.encodeStateAsUpdate(b)])
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(doc, merged)
  const conflict = doc.getMapConflicts()[0]
  t.assert(conflict != null)
  t.assert(conflict.type === 'delete-set')
  t.assert(conflict.key === 'k')
  t.assert(conflict.source === 'remote')
  t.assert(conflict.writes.some(write => write.op === 'delete' || write.snapshot.summary.indexOf('delete') === 0))
  t.assert(conflict.writes.some(write => write.snapshot.summary.indexOf('set') === 0))
}

export const testMixedDeleteSetOnApply = () => {
  const local = new Y.Doc({ mapConflictPolicy: 'collect' })
  const remote = new Y.Doc()
  local.get('m').setAttr('k', 'base')
  Y.applyUpdate(remote, Y.encodeStateAsUpdate(local))
  t.assert(local.getMapConflicts().length === 0)
  local.get('m').deleteAttr('k')
  remote.get('m').setAttr('k', 'remote')
  const diff = Y.encodeStateAsUpdate(remote, Y.encodeStateVector(local))
  Y.applyUpdate(local, diff)
  const conflicts = local.getMapConflicts()
  t.assert(conflicts.length === 1)
  t.assert(conflicts[0].type === 'delete-set' || conflicts[0].type === 'ambiguous')
  t.assert(conflicts[0].source === 'mixed' || conflicts[0].source === 'remote')
  t.assert(conflicts[0].key === 'k')
}

export const testErrorLocalIsAtomic = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  const map = root(doc)
  const nested = map.setAttr('n', new Y.Type())
  nested.setAttr('x', 1)
  const arr = doc.get('arr')
  arr.insert(0, ['keep'])
  let calls = 0
  map.observe(() => {
    calls++
  })
  /** @type {any} */
  let err = null
  try {
    doc.transact(() => {
      nested.setAttr('x', 2)
      arr.insert(0, ['nope'])
      map.setAttr('a', 1)
      map.setAttr('b', 2)
      map.setAttr('a', 3)
    })
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(Array.isArray(err.conflicts))
  t.assert(err.conflicts.length === 1)
  t.assert(err.conflicts[0].type === 'set-set')
  t.assert(err.conflicts[0].writes.every((/** @type {any} */ write) => write.snapshot.summary.length > 0))
  t.assert(map.getAttr('a') === undefined)
  t.assert(map.getAttr('b') === undefined)
  t.assert(nested.getAttr('x') === 1)
  t.assert(map.getAttr('n') === nested)
  t.assert(arr.length === 1)
  t.assert(arr.get(0) === 'keep')
  t.assert(calls === 0)
  t.assert(doc.getMapConflicts().length === 0)
  map.setAttr('after', 1)
  t.assert(map.getAttr('after') === 1)
}

export const testErrorDeleteSetIsAtomic = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  const map = root(doc)
  map.setAttr('keep', 1)
  map.setAttr('k', 'base')
  /** @type {any} */
  let err = null
  try {
    doc.transact(() => {
      map.setAttr('other', 5)
      map.deleteAttr('k')
      map.setAttr('k', 'next')
    })
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(err.conflicts[0].type === 'delete-set')
  t.assert(map.getAttr('keep') === 1)
  t.assert(map.getAttr('k') === 'base')
  t.assert(map.getAttr('other') === undefined)
}

export const testErrorMergedUpdateIsAtomic = () => {
  const run = (/** @type {'set-set'|'delete-set'|'ambiguous'} */ kind) => {
    const a = new Y.Doc()
    const b = new Y.Doc()
    if (kind === 'set-set') {
      a.get('m').setAttr('k', 'a')
      b.get('m').setAttr('k', 'b')
    } else if (kind === 'delete-set') {
      a.get('m').setAttr('k', 'a')
      a.get('m').deleteAttr('k')
      b.get('m').setAttr('k', 'b')
    } else {
      a.get('m').setAttr('k', new Y.Type())
      b.get('m').setAttr('k', new Y.Doc())
    }
    a.get('m').setAttr('from-a', 1)
    b.get('m').setAttr('from-b', 2)
    const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(a), Y.encodeStateAsUpdate(b)])
    const doc = new Y.Doc({ mapConflictPolicy: 'error' })
    const map = doc.get('m')
    map.setAttr('keep', true)
    /** @type {any} */
    let err = null
    try {
      Y.applyUpdate(doc, merged)
    } catch (e) {
      err = e
    }
    t.assert(err instanceof Y.MapConflictError, kind)
    t.assert(Array.isArray(err.conflicts) && err.conflicts.length > 0, kind)
    t.assert(map.getAttr('keep') === true, kind)
    t.assert(map.getAttr('k') === undefined, kind)
    t.assert(map.getAttr('from-a') === undefined, kind)
    t.assert(map.getAttr('from-b') === undefined, kind)
    const conflict = err.conflicts[0]
    t.assert(conflict.writes.every((/** @type {any} */ write) => typeof write.snapshot.summary === 'string' && write.snapshot.summary.length > 0))
    t.assert(typeof conflict.resolution.strategy === 'string')
    t.assert(typeof conflict.resolution.deterministic === 'boolean')
    if (kind === 'ambiguous') {
      t.assert(conflict.type === 'ambiguous' || conflict.ambiguous === true)
    }
  }
  run('set-set')
  run('delete-set')
  run('ambiguous')
}

export const testCausalOverwriteIsNotAConflict = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const map = root(doc)
  map.setAttr('k', 1)
  map.setAttr('k', 2)
  t.assert(doc.getMapConflicts().length === 0)
  t.assert(map.getAttr('k') === 2)
  const remote = new Y.Doc()
  Y.applyUpdate(remote, Y.encodeStateAsUpdate(doc))
  remote.get('m').setAttr('k', 3)
  Y.applyUpdate(doc, Y.encodeStateAsUpdate(remote, Y.encodeStateVector(doc)))
  t.assert(map.getAttr('k') === 3)
  t.assert(doc.getMapConflicts().length === 0)
}

export const testNestedParentId = () => {
  const doc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const nested = doc.get('m').setAttr('child', new Y.Type())
  doc.transact(() => {
    nested.setAttr('k', 1)
    nested.setAttr('k', 2)
  })
  const conflict = doc.getMapConflicts()[0]
  t.assert(conflict.key === 'k')
  t.assert(conflict.parentId !== 'm')
  t.assert(typeof conflict.parentId === 'string' && conflict.parentId.length > 0)
  t.assert(doc.getMapConflictSummary().byParent[conflict.parentId] === 1)
}

export const testErrorNestedApplyRollsBackLocalWrites = () => {
  const a = new Y.Doc()
  const b = new Y.Doc()
  a.get('m').setAttr('k', 'a')
  b.get('m').setAttr('k', 'b')
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(a), Y.encodeStateAsUpdate(b)])
  const doc = new Y.Doc({ mapConflictPolicy: 'error' })
  const map = root(doc)
  map.setAttr('keep', 1)
  /** @type {any} */
  let err = null
  try {
    doc.transact(() => {
      map.setAttr('during', 2)
      Y.applyUpdate(doc, merged)
    })
  } catch (e) {
    err = e
  }
  t.assert(err instanceof Y.MapConflictError)
  t.assert(Array.isArray(err.conflicts) && err.conflicts.length > 0)
  t.assert(map.getAttr('keep') === 1)
  t.assert(map.getAttr('during') === undefined)
  t.assert(map.getAttr('k') === undefined)
  map.setAttr('after', 3)
  t.assert(map.getAttr('after') === 3)
}

export const testDefaultPolicyIsAllow = () => {
  const doc = new Y.Doc()
  t.assert(doc.mapConflictPolicy === 'allow')
  const map = root(doc)
  doc.transact(() => {
    map.setAttr('k', 1)
    map.setAttr('k', 2)
  })
  t.assert(map.getAttr('k') === 2)
  t.assert(doc.getMapConflicts().length === 0)
}
