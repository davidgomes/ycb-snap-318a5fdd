import { getState } from './StructStore.js'
import { applyUpdate } from './encoding.js'

/**
 * @param {Array<import('./MapConflict.js').MapConflict>} conflicts
 */
export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super((conflicts[0] && conflicts[0].message) || 'Map conflict')
    this.name = 'MapConflictError'
    /**
     * @type {Array<MapConflict>}
     */
    this.conflicts = conflicts
  }
}

/**
 * @typedef {Object} MapWrite
 * @property {'set'|'delete'} op
 * @property {string} key
 * @property {string} parentId
 * @property {number} client
 * @property {number} clock
 * @property {boolean} ambiguous
 * @property {{ summary: string }} snapshot
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId
 * @property {string} type
 * @property {boolean} ambiguous
 * @property {'local'|'remote'|'mixed'} source
 * @property {string} message
 * @property {Array<MapWrite>} writes
 * @property {{ winner: string, strategy: string, deterministic: boolean }} resolution
 */

/**
 * @param {any} doc
 * @return {'allow'|'collect'|'error'}
 */
export const mapConflictPolicyOf = doc => {
  const policy = doc.mapConflictPolicy
  if (policy === 'collect' || policy === 'error' || policy === 'allow') return policy
  return 'allow'
}

/**
 * @param {any} parent
 * @return {string}
 */
export const mapParentId = parent => {
  const doc = parent && parent.doc
  if (doc && parent._item == null) {
    for (const [key, value] of doc.share.entries()) {
      if (value === parent) return String(key)
    }
  }
  if (parent && parent._item && parent._item.id) {
    return parent._item.id.client + ':' + parent._item.id.clock
  }
  return 'detached'
}

/**
 * @param {any} value
 * @return {boolean}
 */
const valueIsAmbiguous = value => {
  if (value == null || typeof value !== 'object') return false
  const name = value.constructor && value.constructor.name
  if (name === 'Doc' || name === 'YType') return true
  if (typeof value._integrate === 'function' && value._map instanceof Map) return true
  if (value.store && value.share instanceof Map && typeof value.guid === 'string' && typeof value.clientID === 'number') return true
  return false
}

/**
 * @param {any} content
 * @return {boolean}
 */
const contentIsAmbiguous = content => {
  const name = content && content.constructor && content.constructor.name
  return name === 'ContentType' || name === 'ContentDoc'
}

/**
 * @param {'set'|'delete'} op
 * @param {string} key
 * @param {number} client
 * @param {number} clock
 * @param {any} value
 * @param {boolean} ambiguous
 * @return {string}
 */
const summaryFor = (op, key, client, clock, value, ambiguous) => {
  let rendered = 'null'
  if (op === 'delete') {
    rendered = 'deleted'
  } else if (ambiguous) {
    rendered = 'yjs-type'
  } else {
    try {
      rendered = JSON.stringify(value === undefined ? null : value)
    } catch {
      rendered = String(value)
    }
  }
  if (!rendered) rendered = 'empty'
  return op + ' ' + key + '=' + rendered + ' @' + client + ':' + clock
}

/**
 * @param {any} store
 * @param {{ client: number, clock: number }} id
 * @return {any}
 */
const getItemFromStore = (store, id) => {
  const structs = store.clients.get(id.client)
  if (!structs || structs.length === 0) return null
  let left = 0
  let right = structs.length - 1
  while (left <= right) {
    const mid = (left + right) >> 1
    const struct = structs[mid]
    if (id.clock < struct.id.clock) {
      right = mid - 1
    } else if (id.clock >= struct.id.clock + struct.length) {
      left = mid + 1
    } else {
      return struct
    }
  }
  return null
}

/**
 * @param {any} store
 * @param {any} from
 * @param {any} target
 * @return {boolean}
 */
const reachesOrigin = (store, from, target) => {
  const seen = new Set()
  let origin = from.origin
  while (origin) {
    const mark = origin.client + ':' + origin.clock
    if (seen.has(mark)) return false
    seen.add(mark)
    const item = getItemFromStore(store, origin)
    if (!item || item.id == null) return false
    if (item.id.client === target.id.client && target.id.clock >= item.id.clock && target.id.clock < item.id.clock + item.length) {
      return true
    }
    origin = item.origin
  }
  return false
}

/**
 * @param {any} store
 * @param {any} a
 * @param {any} b
 * @return {boolean}
 */
const itemsAreConcurrent = (store, a, b) => {
  if (a === b) return false
  if (a.id.client === b.id.client && a.id.clock === b.id.clock) return false
  return !reachesOrigin(store, a, b) && !reachesOrigin(store, b, a)
}

/**
 * @param {Array<MapWrite>} writes
 * @return {string}
 */
const winnerOf = writes => {
  const sets = writes.filter(w => w.op !== 'delete')
  const pool = sets.length > 0 ? sets : writes
  let best = pool[0]
  for (let i = 1; i < pool.length; i++) {
    const w = pool[i]
    if (w.client > best.client || (w.client === best.client && w.clock >= best.clock)) {
      best = w
    }
  }
  return best.op + ':' + best.client + ':' + best.clock
}

/**
 * @param {any} doc
 * @param {Array<MapWrite>} writes
 * @param {boolean} localTransaction
 * @return {'local'|'remote'|'mixed'}
 */
const sourceOf = (doc, writes, localTransaction) => {
  let hasLocal = false
  let hasRemote = false
  for (const w of writes) {
    if (w.client === doc.clientID) hasLocal = true
    else hasRemote = true
  }
  if (hasLocal && hasRemote) return 'mixed'
  if (localTransaction && !hasRemote) return 'local'
  if (hasRemote) return 'remote'
  return localTransaction ? 'local' : 'remote'
}

/**
 * @param {any} doc
 * @param {MapConflict} conflict
 */
const recordConflict = (doc, conflict) => {
  if (!doc._mapConflicts) doc._mapConflicts = []
  doc._mapConflicts.push(conflict)
  if (mapConflictPolicyOf(doc) === 'error') {
    throw new MapConflictError([conflict])
  }
}

/**
 * @param {any} doc
 * @param {string} key
 * @param {string} parentId
 * @param {string} pairType
 * @param {Array<MapWrite>} writes
 * @param {boolean} localTransaction
 * @return {MapConflict}
 */
const buildConflict = (doc, key, parentId, pairType, writes, localTransaction) => {
  const ambiguous = writes.some(w => w.ambiguous)
  const type = ambiguous ? 'ambiguous' : pairType
  const source = sourceOf(doc, writes, localTransaction)
  const message = 'Map conflict (' + type + ') on key "' + key + '" in "' + parentId + '" from ' + source + ' writes'
  return {
    key,
    parentId,
    type,
    ambiguous,
    source,
    message,
    writes,
    resolution: {
      winner: winnerOf(writes),
      strategy: 'highest-client-id-then-clock',
      deterministic: true
    }
  }
}

/**
 * @param {any} transaction
 * @return {Map<string, Array<MapWrite>>}
 */
const localWrites = transaction => {
  if (!transaction._localMapWrites) transaction._localMapWrites = new Map()
  return transaction._localMapWrites
}

/**
 * Record a local map set/delete. Throws MapConflictError in error mode before the write is integrated.
 *
 * @param {any} transaction
 * @param {any} parent
 * @param {string} key
 * @param {'set'|'delete'} op
 * @param {any} value
 * @param {any} [existingItem]
 */
export const noteLocalMapWrite = (transaction, parent, key, op, value, existingItem) => {
  const policy = mapConflictPolicyOf(transaction.doc)
  if (policy === 'allow') return
  const parentId = mapParentId(parent)
  const bucketKey = parentId + '\0' + key
  const lists = localWrites(transaction)
  const list = lists.get(bucketKey) || []
  const client = transaction.doc.clientID
  const clock = op === 'delete' && existingItem
    ? existingItem.id.clock
    : getState(transaction.doc.store, client)
  const ambiguous = op === 'delete'
    ? contentIsAmbiguous(existingItem && existingItem.content)
    : valueIsAmbiguous(value)
  /** @type {MapWrite} */
  const write = {
    op,
    key: String(key),
    parentId,
    client,
    clock,
    ambiguous,
    snapshot: {
      summary: summaryFor(op, String(key), client, clock, value, ambiguous)
    }
  }
  const priorDelete = list.filter(w => w.op === 'delete').pop()
  const priorSet = list.filter(w => w.op === 'set').pop()
  list.push(write)
  lists.set(bucketKey, list)
  if (op === 'set' && (priorSet || priorDelete)) {
    const pairType = priorDelete ? 'delete-set' : 'set-set'
    const earlierSet = /** @type {MapWrite} */ (priorSet)
    const writes = priorDelete ? [priorDelete, write] : [earlierSet, write]
    recordConflict(transaction.doc, buildConflict(transaction.doc, String(key), parentId, pairType, writes, true))
  } else if (op === 'delete' && priorSet) {
    recordConflict(transaction.doc, buildConflict(transaction.doc, String(key), parentId, 'delete-set', [priorSet, write], true))
  }
}

/**
 * @param {any} transaction
 * @param {any} item
 */
export const noteRemoteMapInsert = (transaction, item) => {
  if (transaction.local) return
  if (mapConflictPolicyOf(transaction.doc) === 'allow') return
  if (item.parentSub == null) return
  if (!transaction._remoteMapInserts) transaction._remoteMapInserts = []
  transaction._remoteMapInserts.push(item)
}

/**
 * @param {any} transaction
 * @param {any} item
 */
export const noteNetworkMapDelete = (transaction, item) => {
  if (transaction.local) return
  if (mapConflictPolicyOf(transaction.doc) === 'allow') return
  if (!item || item.parentSub == null) return
  if (!transaction._networkMapDeletes) transaction._networkMapDeletes = []
  transaction._networkMapDeletes.push(item)
}

/**
 * @param {any} item
 * @return {MapWrite}
 */
const writeFromItem = (item, op) => {
  let value
  let ambiguous = contentIsAmbiguous(item.content)
  if (op === 'set') {
    try {
      const content = item.content.getContent()
      value = content[content.length - 1]
      if (valueIsAmbiguous(value)) ambiguous = true
    } catch {
      value = undefined
      ambiguous = ambiguous || contentIsAmbiguous(item.content)
    }
  }
  const key = String(item.parentSub)
  const parentId = mapParentId(item.parent)
  return {
    op,
    key,
    parentId,
    client: item.id.client,
    clock: item.id.clock,
    ambiguous,
    snapshot: {
      summary: summaryFor(op, key, item.id.client, item.id.clock, value, ambiguous || op === 'delete')
    }
  }
}

/**
 * Detect concurrent map writes that arrived in one remote transaction / merged update.
 *
 * @param {any} transaction
 */
export const finalizeRemoteMapConflicts = transaction => {
  if (transaction.local) return
  const policy = mapConflictPolicyOf(transaction.doc)
  if (policy === 'allow') return
  const inserts = transaction._remoteMapInserts || []
  const deletes = transaction._networkMapDeletes || []
  if (inserts.length === 0 && deletes.length === 0) return
  const store = transaction.doc.store
  /** @type {Map<string, { parent: any, key: string, parentId: string, inserts: Array<any> }>} */
  const groups = new Map()
  const groupFor = (parent, key) => {
    const parentId = mapParentId(parent)
    const id = parentId + '\0' + key
    let group = groups.get(id)
    if (!group) {
      group = { parent, key: String(key), parentId, inserts: [] }
      groups.set(id, group)
    }
    return group
  }
  for (const item of inserts) {
    groupFor(item.parent, item.parentSub).inserts.push(item)
  }
  for (const group of groups.values()) {
    let head = group.parent._map.get(group.key) || null
    while (head && head.left) head = head.left
    /** @type {Array<any>} */
    const chain = []
    for (let n = head; n; n = n.right) chain.push(n)
    /** @type {Set<any>} */
    const participants = new Set()
    for (const item of group.inserts) {
      for (const other of chain) {
        if (item !== other && itemsAreConcurrent(store, item, other)) {
          participants.add(item)
          participants.add(other)
        }
      }
    }
    const arr = Array.from(participants)
    let concurrent = false
    for (let i = 0; i < arr.length && !concurrent; i++) {
      for (let j = i + 1; j < arr.length; j++) {
        if (itemsAreConcurrent(store, arr[i], arr[j])) {
          concurrent = true
          break
        }
      }
    }
    const keyDeletes = deletes.filter(d => d.parent === group.parent && String(d.parentSub) === group.key)
    const concurrentDelete = keyDeletes.find(d => arr.some(item => itemsAreConcurrent(store, item, d) || (item !== d && !reachesOrigin(store, item, d) && !reachesOrigin(store, d, item))))
    if (!concurrent && !concurrentDelete) continue
    /** @type {Array<MapWrite>} */
    let writes
    let pairType = 'set-set'
    if (concurrentDelete) {
      pairType = 'delete-set'
      const setItem = arr.find(item => item !== concurrentDelete) || arr[0]
      writes = [writeFromItem(concurrentDelete, 'delete'), writeFromItem(setItem, 'set')]
    } else {
      writes = arr.map(item => writeFromItem(item, 'set'))
    }
    const conflict = buildConflict(transaction.doc, group.key, group.parentId, pairType, writes, false)
    recordConflict(transaction.doc, conflict)
  }
}

/**
 * @param {Array<MapConflict>} conflicts
 */
export const summarizeMapConflicts = conflicts => {
  /** @type {Record<string, number>} */
  const byType = {}
  /** @type {Record<string, number>} */
  const byKey = {}
  /** @type {Record<string, number>} */
  const byParent = {}
  /** @type {Record<string, number>} */
  const bySource = {}
  for (const conflict of conflicts) {
    byType[conflict.type] = (byType[conflict.type] || 0) + 1
    byKey[conflict.key] = (byKey[conflict.key] || 0) + 1
    byParent[conflict.parentId] = (byParent[conflict.parentId] || 0) + 1
    bySource[conflict.source] = (bySource[conflict.source] || 0) + 1
  }
  return {
    count: conflicts.length,
    total: conflicts.length,
    byType,
    byKey,
    byParent,
    bySource
  }
}

/**
 * Restore a document to a previously encoded state after a rejected map conflict.
 * Root shared types keep their identity.
 *
 * @param {any} doc
 * @param {Uint8Array} snapshot
 */
export const restoreDocSnapshot = (doc, snapshot) => {
  const clientID = doc.clientID
  const policy = doc.mapConflictPolicy
  const conflicts = doc._mapConflicts
  const hadOwnEmit = Object.prototype.hasOwnProperty.call(doc, 'emit')
  const emit = doc.emit
  doc.emit = () => {}
  doc.mapConflictPolicy = 'allow'
  doc._inMapConflictGuard = true
  const Store = doc.store.constructor
  doc.store = new Store()
  doc.share.forEach(/** @param {any} type */ type => {
    type._map = new Map()
    type._start = null
    type._length = 0
    type._searchMarker = []
  })
  try {
    applyUpdate(doc, snapshot)
  } finally {
    doc.clientID = clientID
    doc.mapConflictPolicy = policy
    doc._mapConflicts = conflicts
    doc._inMapConflictGuard = false
    if (hadOwnEmit) doc.emit = emit
    else delete doc.emit
  }
}
