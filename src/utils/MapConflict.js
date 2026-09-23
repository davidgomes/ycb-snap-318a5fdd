/**
 * Deterministic conflict detection for Y.Map-style attribute writes.
 *
 * `allow` keeps the historical CRDT behavior. `collect` records conflicts and
 * still applies the deterministic winner. `error` throws {@link MapConflictError}
 * before a conflicting local write or merged update is committed.
 */

/**
 * @typedef {'allow' | 'collect' | 'error'} MapConflictPolicy
 */

/**
 * @typedef {'set' | 'delete'} MapWriteKind
 */

/**
 * @typedef {'set-set' | 'delete-set' | 'ambiguous'} MapConflictType
 */

/**
 * @typedef {'local' | 'remote' | 'mixed'} MapConflictSource
 */

/**
 * @typedef {Object} MapConflictWrite
 * @property {MapWriteKind} kind
 * @property {string} key
 * @property {string} parentId
 * @property {number} clientID
 * @property {number} clock
 * @property {boolean} fromUpdate
 * @property {boolean} ambiguous
 * @property {{ summary: string }} snapshot
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId
 * @property {MapConflictType} type
 * @property {boolean} ambiguous
 * @property {MapConflictSource} source
 * @property {string} message
 * @property {Array<MapConflictWrite>} writes
 * @property {{ winner: string, strategy: string, deterministic: boolean }} resolution
 */

/**
 * @param {Array<MapConflict>} conflicts
 */
export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(conflicts.map(conflict => conflict.message).join('; ') || 'Y.Map conflict')
    this.name = 'MapConflictError'
    /**
     * @type {Array<MapConflict>}
     */
    this.conflicts = conflicts
  }
}

/**
 * @param {string} policy
 * @return {policy is MapConflictPolicy}
 */
export const isMapConflictPolicy = (policy) => policy === 'allow' || policy === 'collect' || policy === 'error'

/**
 * @param {MapConflictPolicy} policy
 * @return {boolean}
 */
const policyTracks = (policy) => policy === 'collect' || policy === 'error'

/**
 * @param {import('../utils/StructStore.js').StructStore} store
 * @param {number} client
 * @return {number}
 */
const nextClock = (store, client) => {
  const structs = store.clients.get(client)
  if (structs == null || structs.length === 0) return 0
  const last = structs[structs.length - 1]
  return last.id.clock + last.length
}

/**
 * @param {Array<{ id: { client: number, clock: number }, length: number }>} structs
 * @param {number} clock
 * @return {any}
 */
const findStruct = (structs, clock) => {
  let left = 0
  let right = structs.length - 1
  while (left <= right) {
    const mid = (left + right) >> 1
    const struct = structs[mid]
    if (struct.id.clock <= clock) {
      if (clock < struct.id.clock + struct.length) return struct
      left = mid + 1
    } else {
      right = mid - 1
    }
  }
  return null
}

/**
 * @param {any} value
 * @return {boolean}
 */
const isAmbiguousValue = value => {
  if (value == null || typeof value !== 'object') return false
  if (typeof value.setAttr === 'function' && value._map instanceof Map && typeof value._integrate === 'function') return true
  if (typeof value.transact === 'function' && value.share instanceof Map && value.store != null && typeof value.get === 'function') return true
  return false
}

/**
 * @param {any} content
 * @return {boolean}
 */
const isAmbiguousContent = content => {
  const ref = content.getRef()
  return ref === 7 || ref === 9
}

/**
 * @param {any} value
 * @return {string}
 */
const summarizeValue = value => {
  if (value === undefined) return 'undefined'
  if (value === null) return 'null'
  if (isAmbiguousValue(value)) {
    return typeof value.setAttr === 'function' && value._map instanceof Map ? 'ytype' : 'subdoc'
  }
  const valueType = typeof value
  if (valueType === 'string' || valueType === 'number' || valueType === 'boolean' || valueType === 'bigint') return String(value)
  if (typeof Uint8Array !== 'undefined' && value instanceof Uint8Array) return `binary:${value.byteLength}`
  try {
    return JSON.stringify(value)
  } catch (e) {
    return valueType
  }
}

/**
 * @param {any} content
 * @return {string}
 */
const summarizeContent = content => {
  const ref = content.getRef()
  if (ref === 7) return 'ytype'
  if (ref === 9) return 'subdoc'
  if (ref === 1) return 'deleted'
  try {
    const contentValues = content.getContent()
    return summarizeValue(contentValues[contentValues.length - 1])
  } catch (e) {
    return 'value'
  }
}

/**
 * @param {MapWriteKind} kind
 * @param {string} key
 * @param {string} valueSummary
 * @return {string}
 */
const writeSummary = (kind, key, valueSummary) => kind === 'delete'
  ? `delete ${key}${valueSummary ? `=${valueSummary}` : ''}`
  : `set ${key}=${valueSummary}`

/**
 * @param {import('../ytype.js').YType<any>} parent
 * @return {string}
 */
const parentIdOfType = parent => {
  const item = parent._item
  if (item != null) return item.id.client + ':' + item.id.clock
  const doc = parent.doc
  if (doc != null) {
    for (const [key, value] of doc.share) {
      if (value === parent) return key
    }
  }
  return 'root'
}

/**
 * @param {any} parent
 * @return {string}
 */
const parentIdOfRaw = parent => {
  if (typeof parent === 'string') return parent
  if (parent != null && parent._map instanceof Map && parent._item !== undefined) return parentIdOfType(parent)
  if (parent != null && typeof parent.client === 'number' && typeof parent.clock === 'number') {
    return parent.client + ':' + parent.clock
  }
  return 'root'
}

/**
 * @param {MapConflictWrite} write
 * @return {string}
 */
const writeId = write => `${write.kind}:${write.clientID}:${write.clock}`

/**
 * @param {'set-set' | 'delete-set'} op
 * @param {boolean} ambiguous
 * @param {string} key
 * @param {string} parentId
 * @param {MapConflictSource} source
 * @param {Array<MapConflictWrite>} writes
 * @param {'later-write' | 'higher-client-id' | 'set-over-delete'} strategy
 * @return {MapConflict}
 */
const createConflict = (op, ambiguous, key, parentId, source, writes, strategy) => {
  const type = ambiguous ? 'ambiguous' : op
  let winner = writes[writes.length - 1]
  if (strategy !== 'later-write') {
    const sets = writes.filter(write => write.kind === 'set')
    const pool = sets.length > 0 ? sets : writes
    winner = pool[0]
    for (let i = 1; i < pool.length; i++) {
      const write = pool[i]
      if (write.clientID > winner.clientID || (write.clientID === winner.clientID && write.clock > winner.clock)) {
        winner = write
      }
    }
  }
  return {
    key,
    parentId,
    type,
    ambiguous,
    source,
    message: `Map conflict ${type} on key "${key}" (parent ${parentId}) from ${source} writes`,
    writes,
    resolution: {
      winner: writeId(winner),
      strategy,
      deterministic: true
    }
  }
}

/**
 * @param {import('../utils/Doc.js').Doc} doc
 * @param {Array<MapConflict>} conflicts
 */
export const recordMapConflicts = (doc, conflicts) => {
  if (doc.mapConflictPolicy !== 'collect' || conflicts.length === 0) return
  for (let i = 0; i < conflicts.length; i++) doc._mapConflicts.push(conflicts[i])
}

/**
 * @param {Array<MapConflict>} conflicts
 * @return {{ byType: Record<string, number>, byKey: Record<string, number>, byParent: Record<string, number>, bySource: Record<string, number>, count: number, total: number }}
 */
export const summarizeMapConflicts = conflicts => {
  /**
   * @type {Record<string, number>}
   */
  const byType = {}
  /**
   * @type {Record<string, number>}
   */
  const byKey = {}
  /**
   * @type {Record<string, number>}
   */
  const byParent = {}
  /**
   * @type {Record<string, number>}
   */
  const bySource = {}
  for (let i = 0; i < conflicts.length; i++) {
    const conflict = conflicts[i]
    byType[conflict.type] = (byType[conflict.type] || 0) + 1
    byKey[conflict.key] = (byKey[conflict.key] || 0) + 1
    byParent[conflict.parentId] = (byParent[conflict.parentId] || 0) + 1
    bySource[conflict.source] = (bySource[conflict.source] || 0) + 1
  }
  return {
    byType,
    byKey,
    byParent,
    bySource,
    count: conflicts.length,
    total: conflicts.length
  }
}

/**
 * Record a local attribute write. In `error` mode a conflicting write throws
 * before it is integrated. In `collect` mode the conflict is stored and the
 * write proceeds.
 *
 * @param {import('../utils/Transaction.js').Transaction} transaction
 * @param {import('../ytype.js').YType<any>} parent
 * @param {string} key
 * @param {MapWriteKind} kind
 * @param {any} value
 */
export const noteMapWrite = (transaction, parent, key, kind, value) => {
  const doc = transaction.doc
  if (!policyTracks(doc.mapConflictPolicy)) return
  const current = parent._map.get(key)
  if (kind === 'delete' && (current == null || current.deleted)) return
  const parentId = parentIdOfType(parent)
  const bucket = parentId + '\0' + key
  let writes = transaction._mapWrites.get(bucket)
  if (writes == null) {
    writes = []
    transaction._mapWrites.set(bucket, writes)
  }
  const ambiguous = kind === 'delete'
    ? current != null && isAmbiguousContent(current.content)
    : isAmbiguousValue(value)
  const clientID = kind === 'delete' && current != null ? current.id.client : doc.clientID
  const clock = kind === 'delete' && current != null ? current.id.clock : nextClock(doc.store, doc.clientID)
  const valueSummary = kind === 'delete'
    ? (current != null ? summarizeContent(current.content) : '')
    : summarizeValue(value)
  writes.push({
    kind,
    key,
    parentId,
    clientID,
    clock,
    fromUpdate: !transaction.local,
    ambiguous,
    snapshot: { summary: writeSummary(kind, key, valueSummary) }
  })
  const hasSet = writes.some(write => write.kind === 'set')
  const hasDelete = writes.some(write => write.kind === 'delete')
  /** @type {'set-set' | 'delete-set' | null} */
  let op = null
  if (hasSet && hasDelete) op = 'delete-set'
  else if (writes.filter(write => write.kind === 'set').length >= 2) op = 'set-set'
  if (op == null) return
  const conflict = createConflict(
    op,
    writes.some(write => write.ambiguous),
    key,
    parentId,
    transaction.local ? 'local' : 'remote',
    writes.slice(),
    'later-write'
  )
  const slot = transaction._mapConflictSlots.get(bucket)
  if (slot == null) {
    transaction._mapConflictSlots.set(bucket, conflict)
    if (doc.mapConflictPolicy === 'collect') doc._mapConflicts.push(conflict)
  } else if (doc.mapConflictPolicy === 'collect') {
    const index = doc._mapConflicts.indexOf(slot)
    if (index >= 0) doc._mapConflicts[index] = conflict
    transaction._mapConflictSlots.set(bucket, conflict)
  }
  if (doc.mapConflictPolicy === 'error') throw new MapConflictError([conflict])
}

/**
 * @param {any} struct
 * @return {struct is import('../structs/Item.js').Item}
 */
const isItemStruct = struct => struct != null && struct.content != null && typeof struct.content.getRef === 'function' && struct.id != null

/**
 * @param {import('../utils/Doc.js').Doc} doc
 * @param {{ client: number, clock: number } | null} id
 * @param {Map<number, Array<any>>} incomingByClient
 * @return {any}
 */
const lookupItem = (doc, id, incomingByClient) => {
  if (id == null) return null
  const incoming = incomingByClient.get(id.client)
  if (incoming != null) {
    for (let i = 0; i < incoming.length; i++) {
      const item = incoming[i]
      if (item.id.clock <= id.clock && id.clock < item.id.clock + item.length) return item
    }
  }
  const structs = doc.store.clients.get(id.client)
  if (structs == null || structs.length === 0) return null
  const last = structs[structs.length - 1]
  if (id.clock >= last.id.clock + last.length) return null
  const struct = findStruct(structs, id.clock)
  return isItemStruct(struct) ? struct : null
}

/**
 * @param {any} item
 * @param {{ client: number, clock: number }} id
 * @return {boolean}
 */
const covers = (item, id) => id.client === item.id.client && id.clock >= item.id.clock && id.clock < item.id.clock + item.length

/**
 * @param {any} ancestor
 * @param {any} item
 * @param {function({ client: number, clock: number }): any} lookup
 * @return {boolean}
 */
const isAncestor = (ancestor, item, lookup) => {
  let origin = item.origin
  const seen = new Set()
  while (origin != null) {
    if (covers(ancestor, origin)) return true
    const mark = origin.client + ':' + origin.clock
    if (seen.has(mark)) break
    seen.add(mark)
    const prev = lookup(origin)
    if (prev == null) break
    if (prev === ancestor || covers(ancestor, prev.id)) return true
    origin = prev.origin
  }
  return false
}

/**
 * @param {any} item
 * @param {function({ client: number, clock: number }): any} lookup
 * @return {{ parentId: string, key: string } | null}
 */
const resolveMapAddress = (item, lookup) => {
  if (item.parentSub != null && item.parent != null) {
    return { parentId: parentIdOfRaw(item.parent), key: item.parentSub }
  }
  let origin = item.origin
  const seen = new Set()
  while (origin != null) {
    const mark = origin.client + ':' + origin.clock
    if (seen.has(mark)) break
    seen.add(mark)
    const prev = lookup(origin)
    if (prev == null) break
    if (prev.parentSub != null && prev.parent != null) {
      return { parentId: parentIdOfRaw(prev.parent), key: prev.parentSub }
    }
    origin = prev.origin
  }
  return null
}

/**
 * @param {import('../utils/Doc.js').Doc} doc
 * @param {Array<{ item: any, key: string, parentId: string, fromUpdate: boolean }>} out
 */
const collectExistingMapItems = (doc, out) => {
  const seen = new Set()
  /**
   * @param {import('../ytype.js').YType<any>} type
   */
  const visit = type => {
    if (type == null || seen.has(type)) return
    seen.add(type)
    const parentId = parentIdOfType(type)
    type._map.forEach((item, key) => {
      /** @type {any} */
      let cur = item
      while (cur != null) {
        out.push({ item: cur, key, parentId, fromUpdate: false })
        if (cur.content.getRef() === 7) visit(/** @type {import('../ytype.js').YType<any>} */ (/** @type {any} */ (cur.content).type))
        cur = cur.left
      }
    })
    let child = type._start
    while (child != null) {
      if (isItemStruct(child) && child.content.getRef() === 7) visit(/** @type {import('../ytype.js').YType<any>} */ (/** @type {any} */ (child.content).type))
      child = child.right
    }
  }
  doc.share.forEach(type => visit(type))
}

/**
 * @param {any} item
 * @param {string} key
 * @param {string} parentId
 * @param {MapWriteKind} kind
 * @param {boolean} fromUpdate
 * @return {MapConflictWrite}
 */
const writeFromItem = (item, key, parentId, kind, fromUpdate) => {
  const valueSummary = summarizeContent(item.content)
  return {
    kind,
    key,
    parentId,
    clientID: item.id.client,
    clock: item.id.clock,
    fromUpdate,
    ambiguous: isAmbiguousContent(item.content),
    snapshot: { summary: writeSummary(kind, key, valueSummary) }
  }
}

/**
 * @param {import('../utils/Doc.js').Doc} doc
 * @param {{ client: number, clock: number }} id
 * @return {boolean}
 */
const isKnownStruct = (doc, id) => {
  const structs = doc.store.clients.get(id.client)
  if (structs == null || structs.length === 0) return false
  const last = structs[structs.length - 1]
  return id.clock < last.id.clock + last.length
}

/**
 * Detect set-set and delete-set conflicts inside an update, including conflicts
 * between that update and map entries already stored on `doc`.
 *
 * @param {import('../utils/Doc.js').Doc} doc
 * @param {{ clients: Map<number, { refs: Array<any> }> }} blockSet
 * @param {{ clients: Map<number, { getIds: () => Array<{ clock: number, len: number }> }> }} deleteSet
 * @return {Array<MapConflict>}
 */
export const findRemoteMapConflicts = (doc, blockSet, deleteSet) => {
  /**
   * @type {Map<number, Array<any>>}
   */
  const incomingByClient = new Map()
  blockSet.clients.forEach((blockRange, client) => {
    /**
     * @type {Array<any>}
     */
    const items = []
    const refs = blockRange.refs
    for (let i = 0; i < refs.length; i++) {
      const block = refs[i]
      if (isItemStruct(block) && !isKnownStruct(doc, block.id)) items.push(block)
    }
    if (items.length > 0) incomingByClient.set(client, items)
  })
  /**
   * @param {{ client: number, clock: number }} id
   */
  const lookup = id => lookupItem(doc, id, incomingByClient)
  /**
   * @type {Array<{ item: any, key: string, parentId: string, fromUpdate: boolean }>}
   */
  const entries = []
  collectExistingMapItems(doc, entries)
  incomingByClient.forEach(items => {
    for (let i = 0; i < items.length; i++) {
      const address = resolveMapAddress(items[i], lookup)
      if (address != null) entries.push({ item: items[i], key: address.key, parentId: address.parentId, fromUpdate: true })
    }
  })
  if (entries.length === 0) return []
  /**
   * @type {Map<string, Array<{ item: any, key: string, parentId: string, fromUpdate: boolean }>>}
   */
  const groups = new Map()
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i]
    const bucket = entry.parentId + '\0' + entry.key
    const group = groups.get(bucket)
    if (group == null) groups.set(bucket, [entry])
    else group.push(entry)
  }
  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  groups.forEach(group => {
    if (!group.some(entry => entry.fromUpdate)) return
    const heads = group.filter(entry => !group.some(other => other !== entry && isAncestor(entry.item, other.item, lookup)))
    /**
     * @type {Array<{ item: any, fromUpdate: boolean }>}
     */
    const deleteOps = []
    const seenDeletes = new Set()
    /**
     * @param {any} item
     * @param {boolean} fromUpdate
     */
    const addDelete = (item, fromUpdate) => {
      const mark = item.id.client + ':' + item.id.clock
      if (seenDeletes.has(mark)) return
      seenDeletes.add(mark)
      deleteOps.push({ item, fromUpdate })
    }
    for (let i = 0; i < heads.length; i++) {
      const origin = heads[i].item.origin
      if (origin == null) continue
      const originItem = lookup(origin)
      if (originItem == null || !isItemStruct(originItem)) continue
      if (!originItem.deleted || !isKnownStruct(doc, originItem.id)) continue
      if (group.some(entry => entry.item === originItem && entry.fromUpdate)) continue
      addDelete(originItem, false)
    }
    deleteSet.clients.forEach((ranges, client) => {
      const ids = ranges.getIds()
      const structs = doc.store.clients.get(client) || []
      for (let i = 0; i < ids.length; i++) {
        const start = ids[i].clock
        const end = start + ids[i].len
        for (let s = 0; s < structs.length; s++) {
          const struct = structs[s]
          if (struct.id.clock >= end) break
          if (!isItemStruct(struct) || struct.id.clock + struct.length <= start || struct.deleted) continue
          const address = resolveMapAddress(struct, lookup)
          if (address == null || address.key !== group[0].key || address.parentId !== group[0].parentId) continue
          const implicit = group.some(entry => entry.fromUpdate && entry.item.origin != null && covers(struct, entry.item.origin))
          if (!implicit) addDelete(struct, true)
        }
      }
    })
    const setHeads = heads.filter(entry => !(entry.item.deleted && entry.item.right == null && !entry.fromUpdate))
    const incomingSets = setHeads.filter(entry => entry.fromUpdate)
    /** @type {'set-set' | 'delete-set' | null} */
    let op = null
    /** @type {Array<MapConflictWrite>} */
    let writes = []
    if (setHeads.length >= 2 && incomingSets.length > 0) {
      op = 'set-set'
      writes = setHeads.map(entry => writeFromItem(entry.item, group[0].key, group[0].parentId, 'set', entry.fromUpdate))
    } else if (deleteOps.length > 0 && incomingSets.length > 0) {
      op = 'delete-set'
      writes = deleteOps.map(entry => writeFromItem(entry.item, group[0].key, group[0].parentId, 'delete', entry.fromUpdate))
      for (let i = 0; i < incomingSets.length; i++) {
        writes.push(writeFromItem(incomingSets[i].item, group[0].key, group[0].parentId, 'set', true))
      }
    }
    if (op == null) return
    const fromDoc = writes.some(write => !write.fromUpdate)
    const fromUpdate = writes.some(write => write.fromUpdate)
    /** @type {MapConflictSource} */
    const source = fromDoc && fromUpdate ? 'mixed' : 'remote'
    conflicts.push(createConflict(
      op,
      writes.some(write => write.ambiguous),
      group[0].key,
      group[0].parentId,
      source,
      writes,
      op === 'delete-set' ? 'set-over-delete' : 'higher-client-id'
    ))
  })
  return conflicts
}

/**
 * Undo every struct and delete performed by a transaction that is being rejected
 * because of a map conflict. Cleanup then observes an empty transaction.
 *
 * @param {import('../utils/Transaction.js').Transaction} transaction
 */
export const rollbackMapTransaction = transaction => {
  const doc = transaction.doc
  /**
   * @type {Array<any>}
   */
  const inserted = []
  transaction.insertSet.clients.forEach((ranges, client) => {
    const structs = doc.store.clients.get(client)
    if (structs == null) return
    const ids = ranges.getIds()
    for (let i = 0; i < structs.length; i++) {
      const struct = structs[i]
      const start = struct.id.clock
      const end = start + struct.length
      for (let r = 0; r < ids.length; r++) {
        const rangeStart = ids[r].clock
        const rangeEnd = rangeStart + ids[r].len
        if (start < rangeEnd && end > rangeStart && isItemStruct(struct)) {
          inserted.push(struct)
          break
        }
      }
    }
  })
  inserted.sort((left, right) => right.id.clock - left.id.clock)
  for (let i = 0; i < inserted.length; i++) unlinkInserted(inserted[i])
  transaction.deleteSet.clients.forEach((ranges, client) => {
    const structs = doc.store.clients.get(client)
    if (structs == null) return
    const ids = ranges.getIds()
    for (let i = 0; i < structs.length; i++) {
      const struct = structs[i]
      if (!isItemStruct(struct) || !struct.deleted) continue
      const start = struct.id.clock
      let shouldRestore = false
      for (let r = 0; r < ids.length; r++) {
        if (start >= ids[r].clock && start < ids[r].clock + ids[r].len) {
          shouldRestore = true
          break
        }
      }
      if (!shouldRestore) continue
      let wasInserted = false
      for (let j = 0; j < inserted.length; j++) {
        if (inserted[j] === struct) {
          wasInserted = true
          break
        }
      }
      if (wasInserted) continue
      struct.deleted = false
      const parent = /** @type {any} */ (struct.parent)
      if (parent != null && struct.countable && struct.parentSub == null) parent._length += struct.length
    }
  })
  transaction.insertSet.clients.forEach((ranges, client) => {
    const structs = doc.store.clients.get(client)
    if (structs == null) return
    const ids = ranges.getIds()
    const kept = []
    for (let i = 0; i < structs.length; i++) {
      const struct = structs[i]
      const start = struct.id.clock
      const end = start + struct.length
      let drop = false
      for (let r = 0; r < ids.length; r++) {
        if (start < ids[r].clock + ids[r].len && end > ids[r].clock) {
          drop = true
          break
        }
      }
      if (!drop) kept.push(struct)
    }
    if (kept.length > 0) doc.store.clients.set(client, kept)
    else doc.store.clients.delete(client)
  })
  transaction.insertSet.clients.clear()
  transaction.deleteSet.clients.clear()
  transaction.changed.clear()
  transaction.changedParentTypes.clear()
  transaction._mergeStructs = []
  transaction.subdocsAdded.clear()
  transaction.subdocsRemoved.clear()
  transaction.subdocsLoaded.clear()
  transaction._needFormattingCleanup = false
  transaction._mapWrites.clear()
  transaction._mapConflictSlots.clear()
}

/**
 * @param {any} item
 */
const unlinkInserted = item => {
  const parent = /** @type {any} */ (item.parent)
  if (item.left != null) item.left.right = item.right
  if (item.right != null) item.right.left = item.left
  if (parent != null && parent._start === item) parent._start = item.right
  if (parent != null && item.parentSub != null && parent._map.get(item.parentSub) === item) {
    if (item.left != null) parent._map.set(item.parentSub, item.left)
    else parent._map.delete(item.parentSub)
  }
  if (parent != null && item.parentSub == null && item.countable && !item.deleted) parent._length -= item.length
  if (parent != null && parent._searchMarker != null) parent._searchMarker.length = 0
  if (isItemStruct(item)) {
    const content = /** @type {any} */ (item.content)
    const ref = content.getRef()
    if (ref === 7) {
      content.type.doc = null
      content.type._item = null
    } else if (ref === 9 && content.doc != null) {
      content.doc._item = null
    }
  }
  item.left = null
  item.right = null
}
