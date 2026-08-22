/**
 * @typedef {'set'|'delete'} MapWriteOperation
 */

/**
 * @typedef {Object} MapWrite
 * @property {MapWriteOperation} operation
 * @property {string} key
 * @property {string} parentId
 * @property {string} id
 * @property {boolean} local
 * @property {boolean} ambiguous
 * @property {any} item
 * @property {any} previousItem
 * @property {{summary:string}} snapshot
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId
 * @property {string} type
 * @property {boolean} [ambiguous]
 * @property {'local'|'remote'|'mixed'} source
 * @property {string} message
 * @property {Array<MapWrite>} writes
 * @property {{winner:string, strategy:string, deterministic:boolean}} resolution
 */

export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(`Map conflicts detected (${conflicts.length})`)
    this.name = 'MapConflictError'
    this.conflicts = conflicts
  }
}

/**
 * @param {any} value
 * @return {boolean}
 */
export const isMapConflictError = value => value instanceof MapConflictError

/**
 * @param {any} value
 * @return {boolean}
 */
const isAmbiguousValue = value => {
  const name = value?.constructor?.name
  return name === 'ContentType' || name === 'ContentDoc' || name === 'YType' || name === 'Doc'
}

/**
 * @param {any} id
 * @return {string}
 */
const idToString = id => id == null ? 'unknown' : `${id.client}:${id.clock}`

/**
 * @param {any} parent
 * @param {any} item
 * @return {string}
 */
export const getMapParentId = (parent, item = null) => {
  if (typeof parent === 'string') return parent
  if (parent && parent._item) return idToString(parent._item.id)
  if (parent?.doc?.share) {
    for (const [key, value] of parent.doc.share) {
      if (value === parent) return key
    }
  }
  if (parent && typeof parent.client === 'number') return idToString(parent)
  if (item?.parent && typeof item.parent === 'string') return item.parent
  return idToString(item?.parent)
}

/**
 * @param {any} item
 * @return {boolean}
 */
const isMapItem = item => item?.parentSub !== null && item?.parentSub !== undefined

/**
 * @param {any} item
 * @param {boolean} local
 * @param {MapWriteOperation} operation
 * @param {any} value
 * @return {MapWrite}
 */
const createWrite = (item, local, operation, value) => {
  const key = item?.parentSub ?? value?.key ?? ''
  const parentId = getMapParentId(item?.parent, item)
  const id = item ? idToString(item.id) : `${local ? 'local' : 'remote'}:${parentId}:${key}`
  return {
    operation,
    key,
    parentId,
    id,
    local,
    ambiguous: isAmbiguousValue(value) || isAmbiguousValue(item?.content),
    item,
    previousItem: null,
    snapshot: { summary: `${operation} ${parentId}.${key} (${id})` }
  }
}

/**
 * @param {Array<MapWrite>} writes
 * @return {boolean}
 */
const hasConflict = writes => {
  for (let i = 0; i < writes.length; i++) {
    for (let j = i + 1; j < writes.length; j++) {
      const left = writes[i].operation
      const right = writes[j].operation
      if ((left === 'set' && right === 'set') ||
          (left === 'set' && right === 'delete') ||
          (left === 'delete' && right === 'set')) {
        return true
      }
    }
  }
  return false
}

/**
 * @param {Array<MapWrite>} writes
 * @return {MapConflict}
 */
const createConflict = writes => {
  const first = writes[0]
  const ambiguous = writes.some(write => write.ambiguous)
  const type = ambiguous
    ? 'ambiguous'
    : writes.some(write => write.operation === 'delete') ? 'delete-set' : 'set-set'
  const sources = new Set(writes.map(write => write.local ? 'local' : 'remote'))
  const source = /** @type {'local'|'remote'|'mixed'} */ (sources.size === 1 ? [...sources][0] : 'mixed')
  const ordered = writes.slice().sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
  const winner = ordered[ordered.length - 1].id
  const conflict = /** @type {MapConflict} */ ({
    key: first.key,
    parentId: first.parentId,
    type,
    source,
    message: `Conflicting ${type} writes for map key "${first.key}" on "${first.parentId}"`,
    writes,
    resolution: {
      winner,
      strategy: 'deterministic client-clock order',
      deterministic: true
    }
  })
  if (ambiguous) conflict.ambiguous = true
  return conflict
}

/**
 * @param {any} transaction
 * @param {any} parent
 * @param {string} key
 * @param {MapWriteOperation} operation
 * @param {any} value
 * @param {any} item
 * @return {MapWrite|null}
 */
export const recordMapWrite = (transaction, parent, key, operation, value = null, item = null) => {
  const doc = transaction.doc
  if (doc.mapConflictPolicy === 'allow') return null
  const write = createWrite(item || { parent, parentSub: key }, transaction.local, operation, value)
  write.key = key
  write.parentId = getMapParentId(parent, item)
  write.previousItem = parent?._map?.get(key) || null
  transaction._mapWrites ||= []
  transaction._mapWriteItems ||= new Set()
  const related = transaction._mapWrites.filter(/** @param {MapWrite} previous */ previous =>
    previous.key === write.key && previous.parentId === write.parentId)
  const current = operation === 'set' && parent?._map?.get(key)
  if (
    current &&
    current !== item &&
    !transaction.local &&
    !related.some(/** @param {MapWrite} previous */ previous => previous.item === current) &&
    item?.origin &&
    (current.lastId?.client !== item.origin.client || current.lastId?.clock !== item.origin.clock)
  ) {
    related.push(createWrite(current, false, 'set', current.content))
  }
  const conflictWrites = [...related, write].filter((candidate, index, all) =>
    all.findIndex(other => other.id === candidate.id && other.operation === candidate.operation) === index)
  transaction._mapWrites.push(write)
  if (!hasConflict(conflictWrites)) return write

  const conflict = createConflict(conflictWrites)
  if (doc.mapConflictPolicy === 'error') {
    throw new MapConflictError([conflict])
  }
  const conflictKey = `${write.parentId}\u0000${write.key}`
  transaction._mapConflictGroups ||= new Map()
  const existing = transaction._mapConflictGroups.get(conflictKey)
  if (existing) {
    existing.writes = [...existing.writes, write].filter((candidate, index, all) =>
      all.findIndex(other => other.id === candidate.id && other.operation === candidate.operation) === index)
    const updated = createConflict(existing.writes)
    Object.assign(existing, updated)
  } else {
    transaction._mapConflictGroups.set(conflictKey, conflict)
    doc._mapConflicts.push(conflict)
  }
  return write
}

/**
 * @param {any} transaction
 * @param {any} item
 */
export const markMapWriteItem = (transaction, item) => {
  transaction._mapWriteItems ||= new Set()
  transaction._mapWriteItems.add(item)
}

/**
 * @param {any} transaction
 * @param {any} blockSet
 */
export const recordMapWritesFromBlockSet = (transaction, blockSet) => {
  if (transaction.doc.mapConflictPolicy === 'allow') return
  blockSet.clients.forEach(/** @param {any} range */ range => {
    range.refs.forEach(/** @param {any} item */ item => {
      if (item?.constructor?.name === 'Item' && isMapItem(item)) {
        const write = recordMapWrite(transaction, null, item.parentSub, 'set', item.content, item)
        if (write) markMapWriteItem(transaction, item)
      }
    })
  })
}

/**
 * @param {any} transaction
 * @param {any} deleteSet
 * @param {any} blockSet
 */
export const recordMapDeletesFromSet = (transaction, deleteSet, blockSet) => {
  if (transaction.doc.mapConflictPolicy === 'allow') return
  const candidates = new Map()
  blockSet?.clients.forEach(/** @param {any} range @param {number} client */ (range, client) => {
    range.refs.forEach(/** @param {any} item */ item => candidates.set(`${client}:${item.id.clock}`, item))
  })
  deleteSet.forEach(/** @param {any} range @param {number} client */ (range, client) => {
    const structs = transaction.doc.store.clients.get(client) || []
    for (let clock = range.clock; clock < range.clock + range.len; clock++) {
      const item = candidates.get(`${client}:${clock}`) || structs.find(/** @param {any} struct */ struct =>
        struct.id.clock <= clock && clock < struct.id.clock + struct.length)
      if (item && item.constructor?.name === 'Item' && isMapItem(item)) {
        const write = recordMapWrite(transaction, item.parent, item.parentSub, 'delete', item.content, item)
        if (write) markMapWriteItem(transaction, item)
      }
    }
  })
}

/**
 * Roll back a local transaction after a strict conflict was found.
 *
 * @param {any} transaction
 */
export const abortMapTransaction = transaction => {
  /** @type {Array<any>} */
  const inserted = []
  transaction.insertSet.clients.forEach(/** @param {any} idRanges @param {number} client */ (idRanges, client) => {
    const structs = transaction.doc.store.clients.get(client) || []
    for (const item of structs) {
      if (item.constructor?.name !== 'Item') continue
      if (idRanges.getIds().some(/** @param {any} range */ range =>
        item.id.clock < range.clock + range.len && item.id.clock + item.length > range.clock)) {
        inserted.push(item)
      }
    }
  })
  for (let i = inserted.length - 1; i >= 0; i--) {
    const item = inserted[i]
    const parent = item.parent
    if (parent && typeof parent !== 'string') {
      if (!item.deleted && item.countable && item.parentSub === null) {
        parent._length -= item.length
      }
      if (item.left?.right === item) item.left.right = item.right
      if (item.right?.left === item) item.right.left = item.left
      if (parent._start === item) parent._start = item.right
      if (item.parentSub !== null && parent._map?.get(item.parentSub) === item) {
        parent._map.delete(item.parentSub)
      }
    }
    item.deleted = true
  }
  transaction.doc.store.clients.forEach(/** @param {any} structs @param {number} client */ (structs, client) => {
    const ids = transaction.insertSet.clients.get(client)
    if (ids) {
      transaction.doc.store.clients.set(client, structs.filter(/** @param {any} item */ item =>
        !ids.getIds().some(/** @param {any} range */ range =>
          item.id.clock < range.clock + range.len && item.id.clock + item.length > range.clock)))
    }
  })
  for (let i = transaction._mapWrites.length - 1; i >= 0; i--) {
    const write = transaction._mapWrites[i]
    const parent = write.item?.parent
    if (parent && typeof parent !== 'string' && parent._map) {
      if (write.previousItem) {
        write.previousItem.deleted = false
        parent._map.set(write.key, write.previousItem)
      } else {
        parent._map.delete(write.key)
      }
    }
  }
  transaction.insertSet.clients.clear()
  transaction.deleteSet.clients.clear()
  transaction.changed.clear()
  transaction.changedParentTypes.clear()
  transaction._mergeStructs.length = 0
  transaction._mapWrites.length = 0
  transaction._mapWriteItems.clear()
}
