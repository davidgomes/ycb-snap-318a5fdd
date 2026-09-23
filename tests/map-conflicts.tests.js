import * as Y from '../src/index.js'
import * as t from 'lib0/testing'
import * as delta from 'lib0/delta'

/**
 * @param {Y.Doc} ydoc
 * @param {function(Y.Doc):void} f
 * @return {Uint8Array<ArrayBuffer>}
 */
const captureUpdate = (ydoc, f) => {
  const sv = Y.encodeStateVector(ydoc)
  f(ydoc)
  return Y.encodeStateAsUpdate(ydoc, sv)
}

/**
 * @param {any} conflict
 */
const validateConflictShape = conflict => {
  t.assert(typeof conflict.key === 'string')
  t.assert(typeof conflict.parentId === 'string')
  t.assert(conflict.type === 'set-set' || conflict.type === 'delete-set')
  t.assert(typeof conflict.ambiguous === 'boolean')
  t.assert(['local', 'remote', 'mixed'].includes(conflict.source))
  t.assert(typeof conflict.message === 'string' && conflict.message.length > 0)
  t.assert(Array.isArray(conflict.writes) && conflict.writes.length >= 2)
  conflict.writes.forEach(/** @param {any} w */ w => {
    t.assert(w.op === 'set' || w.op === 'delete')
    t.assert(w.source === 'local' || w.source === 'remote')
    t.assert(typeof w.snapshot.summary === 'string' && w.snapshot.summary.length > 0)
  })
  t.assert(typeof conflict.resolution === 'object' && 'winner' in conflict.resolution)
  t.assert(typeof conflict.resolution.strategy === 'string')
  t.assert(typeof conflict.resolution.deterministic === 'boolean')
}

/**
 * @param {t.TestCase} _tc
 */
export const testInvalidPolicy = _tc => {
  t.fails(() => {
    new Y.Doc({ mapConflictPolicy: /** @type {any} */ ('strict') }) // eslint-disable-line no-new
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
    ymap.setAttr('b', 1)
    ymap.deleteAttr('b')
  })
  const docA = new Y.Doc()
  const docB = new Y.Doc()
  docA.get('map').setAttr('c', 'A')
  docB.get('map').setAttr('c', 'B')
  Y.applyUpdate(ydoc, Y.mergeUpdates([Y.encodeStateAsUpdate(docA), Y.encodeStateAsUpdate(docB)]))
  t.compare(ymap.getAttr('a'), 2)
  t.assert(ymap.getAttr('b') === undefined)
  t.assert(ymap.getAttr('c') === 'A' || ymap.getAttr('c') === 'B')
  t.assert(ydoc.getMapConflicts().length === 0)
  t.assert(ydoc.getMapConflictSummary().count === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectLocalConflicts = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const ymap = ydoc.get('map')
  ydoc.transact(() => {
    ymap.setAttr('a', 1)
    ymap.setAttr('a', 2)
    ymap.setAttr('b', 1)
    ymap.deleteAttr('b')
    ymap.setAttr('c', 1)
  })
  ymap.setAttr('d', 1)
  ymap.deleteAttr('d')
  ymap.setAttr('d', 2)
  t.compare(ymap.getAttr('a'), 2)
  t.assert(ymap.getAttr('b') === undefined)
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 2)
  conflicts.forEach(validateConflictShape)
  const [a, b] = conflicts
  t.assert(a.key === 'a' && a.type === 'set-set' && a.source === 'local' && a.parentId === 'map' && !a.ambiguous)
  t.assert(a.resolution.winner === a.writes[1])
  t.assert(a.resolution.deterministic)
  t.assert(b.key === 'b' && b.type === 'delete-set')
  t.assert(b.resolution.winner?.op === 'delete')
  const summary = ydoc.getMapConflictSummary()
  t.assert(summary.count === 2 && summary.total === 2)
  t.assert(summary.byType['set-set'] === 1 && summary.byType['delete-set'] === 1)
  t.assert(summary.byKey.a === 1 && summary.byKey.b === 1)
  t.assert(summary.byParent.map === 2)
  t.assert(summary.bySource.local === 2)
  ydoc.clearMapConflicts()
  t.assert(ydoc.getMapConflicts().length === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectDeleteThenSet = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const ymap = ydoc.get('map')
  ymap.setAttr('k', 1)
  ydoc.transact(() => {
    ymap.deleteAttr('k')
    ymap.setAttr('k', 2)
  })
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 1)
  t.assert(conflicts[0].type === 'delete-set')
  t.compare(conflicts[0].writes.map(w => w.op), ['delete', 'set'])
  t.assert(conflicts[0].resolution.winner === conflicts[0].writes[1])
}

/**
 * @param {t.TestCase} _tc
 */
export const testNestedParentId = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const inner = ydoc.get('map').setAttr('inner', new Y.Type())
  ydoc.transact(() => {
    inner.setAttr('x', 1)
    inner.setAttr('x', 2)
  })
  const [conflict] = ydoc.getMapConflicts()
  const item = /** @type {Y.Item} */ (inner._item)
  t.assert(conflict.parentId === `${item.id.client}:${item.id.clock}`)
  t.assert(ydoc.getMapConflictSummary().byParent[conflict.parentId] === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectRemoteMergedSetSet = _tc => {
  const docA = new Y.Doc()
  const docB = new Y.Doc()
  docA.get('map').setAttr('k', 'A')
  docB.get('map').setAttr('k', 'B')
  const merged = Y.mergeUpdates([Y.encodeStateAsUpdate(docA), Y.encodeStateAsUpdate(docB)])
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(ydoc, merged)
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 1)
  validateConflictShape(conflicts[0])
  t.assert(conflicts[0].type === 'set-set' && conflicts[0].source === 'remote')
  const winner = conflicts[0].resolution.winner
  t.assert(winner !== null && winner.snapshot.summary === `set "k" = ${JSON.stringify(ydoc.get('map').getAttr('k'))}`)
  // conflict detection and resolution is deterministic
  const ydoc2 = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(ydoc2, merged)
  t.compare(ydoc2.getMapConflicts(), conflicts)
}

/**
 * @param {t.TestCase} _tc
 */
export const testCollectRemoteMergedDeleteSet = _tc => {
  const docA = new Y.Doc()
  const u1 = captureUpdate(docA, d => d.get('map').setAttr('k', 1))
  const u2 = captureUpdate(docA, d => d.get('map').deleteAttr('k'))
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(ydoc, Y.mergeUpdates([u1, u2]))
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 1)
  validateConflictShape(conflicts[0])
  t.assert(conflicts[0].type === 'delete-set' && conflicts[0].source === 'remote')
  t.assert(conflicts[0].resolution.winner?.op === 'delete')
  t.assert(ydoc.get('map').getAttr('k') === undefined)
}

/**
 * Regular syncing of sequential changes must not produce conflicts.
 *
 * @param {t.TestCase} _tc
 */
export const testSequentialRemoteUpdatesDoNotConflict = _tc => {
  const docA = new Y.Doc()
  const ydoc = new Y.Doc({ mapConflictPolicy: 'error' })
  docA.on('update', update => { Y.applyUpdate(ydoc, update) })
  const ymapA = docA.get('map')
  ymapA.setAttr('k', 1)
  ymapA.setAttr('k', 2)
  ymapA.deleteAttr('k')
  ymapA.setAttr('k', 3)
  docA.transact(() => {
    ymapA.setAttr('x', 1)
    ymapA.setAttr('y', 1)
  })
  t.compare(ydoc.get('map').toJSON(), ymapA.toJSON())
}

/**
 * @param {t.TestCase} _tc
 */
export const testAmbiguousConflicts = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  const ymap = ydoc.get('map')
  ydoc.transact(() => {
    ymap.setAttr('type', new Y.Type())
    ymap.setAttr('type', 'plain')
    ymap.setAttr('subdoc', new Y.Doc())
    ymap.deleteAttr('subdoc')
    ymap.setAttr('plain', 1)
    ymap.setAttr('plain', 2)
  })
  const conflicts = ydoc.getMapConflicts()
  t.assert(conflicts.length === 3)
  const byKey = Object.fromEntries(conflicts.map(c => [c.key, c]))
  t.assert(byKey.type.ambiguous && byKey.type.writes[0].ambiguous && !byKey.type.writes[1].ambiguous)
  t.assert(byKey.subdoc.ambiguous && byKey.subdoc.type === 'delete-set')
  t.assert(!byKey.plain.ambiguous)
  t.assert(!byKey.type.resolution.deterministic)
  t.assert(ydoc.getMapConflictSummary().ambiguous === 2)
  // remote conflicts involving types are ambiguous as well
  const docA = new Y.Doc()
  const docB = new Y.Doc()
  docA.get('map').setAttr('k', new Y.Type())
  docB.get('map').setAttr('k', new Y.Doc())
  const remote = new Y.Doc({ mapConflictPolicy: 'collect' })
  Y.applyUpdate(remote, Y.mergeUpdates([Y.encodeStateAsUpdate(docA), Y.encodeStateAsUpdate(docB)]))
  const [rc] = remote.getMapConflicts()
  t.assert(rc.ambiguous && rc.writes.every(w => w.ambiguous))
}

/**
 * @param {t.TestCase} _tc
 */
export const testMixedSource = _tc => {
  const docA = new Y.Doc()
  docA.get('map').setAttr('k', 'remote')
  const update = Y.encodeStateAsUpdate(docA)
  const ydoc = new Y.Doc({ mapConflictPolicy: 'collect' })
  ydoc.transact(() => {
    ydoc.get('map').setAttr('k', 'local')
    Y.applyUpdate(ydoc, update)
  })
  const [conflict] = ydoc.getMapConflicts()
  t.assert(conflict.source === 'mixed')
  t.compare(conflict.writes.map(w => w.source), ['local', 'remote'])
  t.assert(ydoc.getMapConflictSummary().bySource.mixed === 1)
}

/**
 * @param {t.TestCase} _tc
 */
export const testErrorLocal = _tc => {
  const ydoc = new Y.Doc({ mapConflictPolicy: 'error' })
  const ymap = ydoc.get('map')
  ymap.setAttr('a', 1)
  ymap.setAttr('a', 2) // separate transactions don't conflict
  /**
   * @param {function():void} f
   * @return {Y.MapConflictError}
   */
  const expectConflict = f => {
    try {
      f()
    } catch (err) {
      t.assert(err instanceof Y.MapConflictError)
      t.assert(/** @type {Y.MapConflictError} */ (err).name === 'MapConflictError')
      return /** @type {Y.MapConflictError} */ (err)
    }
    t.fail('expected a MapConflictError')
    throw new Error()
  }
  const setSet = expectConflict(() => ydoc.transact(() => {
    ymap.setAttr('a', 3)
    ymap.setAttr('a', 4)
  }))
  t.assert(setSet.conflicts.length === 1 && setSet.conflicts[0].type === 'set-set')
  validateConflictShape(setSet.conflicts[0])
  t.assert(setSet.conflicts[0].resolution.winner === null && setSet.conflicts[0].resolution.strategy === 'reject')
  t.assert(ymap.getAttr('a') === 3)
  const deleteSet = expectConflict(() => ydoc.transact(() => {
    ymap.deleteAttr('a')
    ymap.setAttr('a', 5)
  }))
  t.assert(deleteSet.conflicts[0].type === 'delete-set')
  t.assert(ymap.getAttr('a') === undefined)
  // a single delta is rejected as a whole
  ydoc.transact(() => {
    ymap.setAttr('x', 1)
    expectConflict(() => ymap.applyDelta(delta.create().setAttr('y', 1).setAttr('x', 2).done()))
  })
  t.assert(ymap.getAttr('x') === 1 && ymap.getAttr('y') === undefined)
  t.assert(ydoc.getMapConflicts().length === 0)
}

/**
 * @param {t.TestCase} _tc
 */
export const testErrorRemoteIsAtomic = _tc => {
  const docA = new Y.Doc()
  const docB = new Y.Doc()
  const docC = new Y.Doc()
  const ymapA = docA.get('map')
  const ymapB = docB.get('map')
  const setSet = Y.mergeUpdates([
    captureUpdate(docA, () => { ymapA.setAttr('k', 'A'); ymapA.setAttr('other', 1) }),
    captureUpdate(docB, () => { ymapB.setAttr('k', 'B'); ymapB.setAttr('unrelated', 2) })
  ])
  const setDelete = Y.mergeUpdates([
    captureUpdate(docC, d => d.get('map').setAttr('j', 1)),
    captureUpdate(docC, d => d.get('map').deleteAttr('j'))
  ])
  /**
   * @type {Uint8Array|null}
   */
  let sameTransaction = null
  const docD = new Y.Doc()
  docD.on('update', update => { sameTransaction = update })
  docD.transact(() => {
    docD.get('map').setAttr('z', 1)
    docD.get('map').deleteAttr('z')
  })
  const nested = Y.mergeUpdates([
    captureUpdate(docA, () => { ymapA.setAttr('nested', new Y.Type()) }),
    captureUpdate(docB, () => { ymapB.setAttr('nested', 'plain') })
  ])
  const cases = [
    { update: setSet, type: 'set-set' },
    { update: setDelete, type: 'delete-set' },
    { update: /** @type {any} */ (sameTransaction), type: 'delete-set' },
    { update: nested, type: 'set-set', ambiguous: true }
  ]
  cases.forEach(({ update, type, ambiguous = false }) => {
    const ydoc = new Y.Doc({ mapConflictPolicy: 'error' })
    ydoc.get('map').setAttr('existing', true)
    const before = Y.encodeStateAsUpdate(ydoc)
    let emitted = false
    ydoc.on('update', () => { emitted = true })
    try {
      Y.applyUpdate(ydoc, update)
      t.fail('expected a MapConflictError')
    } catch (err) {
      t.assert(err instanceof Y.MapConflictError)
      const conflicts = /** @type {Y.MapConflictError} */ (err).conflicts
      t.assert(conflicts.length === 1 && conflicts[0].type === type && conflicts[0].source === 'remote')
      t.assert(conflicts[0].ambiguous === ambiguous)
      validateConflictShape(conflicts[0])
    }
    t.assert(!emitted)
    t.compare(Y.encodeStateAsUpdate(ydoc), before)
    t.compare(ydoc.get('map').toJSON(), { attrs: { existing: true } })
    t.assert(ydoc.store.pendingStructs === null && ydoc.store.pendingDs === null)
    // the same update applies normally with policy `allow`
    const allowed = new Y.Doc()
    Y.applyUpdate(allowed, update)
  })
}
