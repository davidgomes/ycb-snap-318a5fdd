import {
  ContentAny,
  ContentBinary,
  ContentDeleted,
  ContentDoc,
  ContentType,
  createID,
  decodeUpdateV2,
  findIndexSS,
  findRootTypeKey,
  getState,
  Item,
  UpdateDecoderV1, UpdateDecoderV2, Doc, Transaction, ID, AbstractContent, StructStore // eslint-disable-line
} from '../internals.js'

import { YType } from '../ytype.js' // eslint-disable-line

/**
 * @typedef {'allow'|'collect'|'error'} MapConflictPolicy
 */

/**
 * @typedef {'set-set'|'delete-set'} MapConflictKind
 */

/**
 * @typedef {Object} MapWriteSnapshot
 * @property {string} summary Human readable, non-empty description of the write
 * @property {string} kind Kind of the written (or deleted) content: any|binary|type|doc|deleted|unknown
 * @property {any} [value] The written value (only for plain JSON-like values)
 */

/**
 * @typedef {Object} MapWrite
 * @property {'set'|'delete'} op
 * @property {'local'|'remote'} source
 * @property {string} id The id of the created item (set) or of the deleted item (delete), formatted as "client:clock"
 * @property {number} client
 * @property {number} clock
 * @property {string|null} origin For sets: the id of the entry this write was based on
 * @property {boolean} ambiguous Whether a Yjs type or subdocument is involved
 * @property {MapWriteSnapshot} snapshot
 */

/**
 * @typedef {Object} MapConflictResolution
 * @property {MapWrite} winner The write whose effect is visible after the conflict is resolved
 * @property {string} strategy
 * @property {boolean} deterministic
 */

/**
 * @typedef {Object} MapConflict
 * @property {MapConflictKind|'ambiguous'} type
 * @property {MapConflictKind} kind The underlying conflict kind, also available when type is 'ambiguous'
 * @property {boolean} ambiguous
 * @property {string} key
 * @property {string} parentId Root type name, or "client:clock" of the item that holds a nested type
 * @property {'local'|'remote'|'mixed'} source
 * @property {string} message
 * @property {Array<MapWrite>} writes
 * @property {MapConflictResolution} resolution
 */

/**
 * @typedef {Object} MapConflictSummary
 * @property {number} count
 * @property {number} total
 * @property {number} ambiguous
 * @property {Object<string,number>} byType
 * @property {Object<string,number>} byKey
 * @property {Object<string,number>} byParent
 * @property {Object<string,number>} bySource
 */

/**
 * @typedef {{ parentId: string, key: string, writes: Array<MapWrite> }} MapWriteGroup
 */

/**
 * @type {Array<MapConflictPolicy>}
 */
export const mapConflictPolicies = ['allow', 'collect', 'error']

/**
 * @param {any} policy
 * @return {MapConflictPolicy}
 */
export const validateMapConflictPolicy = policy => {
  if (!mapConflictPolicies.includes(policy)) {
    throw new TypeError(`Invalid mapConflictPolicy "${policy}". Expected one of: ${mapConflictPolicies.join(', ')}`)
  }
  return policy
}

export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(`${conflicts.length} map conflict${conflicts.length === 1 ? '' : 's'} detected: ${conflicts.map(c => c.message).join(' | ')}`)
    this.name = 'MapConflictError'
    this.conflicts = conflicts
  }
}

/**
 * @param {ID} id
 */
const idToString = id => `${id.client}:${id.clock}`

/**
 * @param {string} parentId
 * @param {string} key
 */
const groupKey = (parentId, key) => parentId + '\u0000' + key

/**
 * @param {YType} type
 * @return {string}
 */
const parentIdOfType = type => type._item !== null ? idToString(type._item.id) : findRootTypeKey(type)

/**
 * @param {any} value
 */
const stringifyValue = value => {
  let s
  try {
    s = JSON.stringify(value)
  } catch (_e) {}
  if (s === undefined) s = String(value)
  return s.length > 80 ? s.slice(0, 77) + '...' : s
}

/**
 * @param {AbstractContent} content
 * @param {number} offset
 * @return {MapWriteSnapshot}
 */
const describeContent = (content, offset) => {
  switch (content.constructor) {
    case ContentAny: {
      const value = /** @type {ContentAny} */ (content).arr[offset]
      return { summary: stringifyValue(value), kind: 'any', value }
    }
    case ContentBinary:
      return { summary: `<Uint8Array(${/** @type {ContentBinary} */ (content).content.length})>`, kind: 'binary' }
    case ContentType: {
      const name = /** @type {ContentType} */ (content).type.name
      return { summary: name ? `<Y.Type ${name}>` : '<Y.Type>', kind: 'type' }
    }
    case ContentDoc:
      return { summary: `<Y.Doc ${/** @type {ContentDoc} */ (content).doc.guid}>`, kind: 'doc' }
    case ContentDeleted:
      return { summary: '<overwritten value>', kind: 'deleted' }
    default:
      return { summary: `<${content.constructor.name}>`, kind: 'unknown' }
  }
}

/**
 * @param {'set'|'delete'} op
 * @param {'local'|'remote'} source
 * @param {ID} id
 * @param {string|null} origin
 * @param {string} key
 * @param {MapWriteSnapshot} described
 * @return {MapWrite}
 */
const createWrite = (op, source, id, origin, key, described) => {
  const snapshot = { ...described }
  snapshot.summary = op === 'set' ? `set "${key}" = ${described.summary}` : `delete "${key}" (was ${described.summary})`
  return {
    op,
    source,
    id: idToString(id),
    client: id.client,
    clock: id.clock,
    origin,
    ambiguous: described.kind === 'type' || described.kind === 'doc',
    snapshot
  }
}

/**
 * Compute which write is visible after Yjs integrated all writes. Sets form a tree via their
 * origin. Among concurrent branches Yjs keeps the item of the higher client id to the right, and
 * the right-most item is the map value.
 *
 * @param {Array<MapWrite>} writes
 * @return {MapConflictResolution}
 */
const resolveWrites = writes => {
  const sets = writes.filter(w => w.op === 'set')
  const setsById = new Map(sets.map(w => [w.id, w]))
  const overwritten = new Set(sets.map(w => w.origin))
  const leaves = sets.filter(w => !overwritten.has(w.id))
  /**
   * @param {MapWrite} w
   */
  const pathOf = w => {
    const path = [w]
    const seen = new Set([w.id])
    let curr = w
    while (curr.origin !== null && setsById.has(curr.origin) && !seen.has(curr.origin)) {
      curr = /** @type {MapWrite} */ (setsById.get(curr.origin))
      seen.add(curr.id)
      path.unshift(curr)
    }
    return path
  }
  /**
   * @param {MapWrite} a
   * @param {MapWrite} b
   */
  const cmpWrite = (a, b) => a.client !== b.client ? a.client - b.client : a.clock - b.clock
  let winner = leaves[0]
  let winnerPath = pathOf(winner)
  for (let i = 1; i < leaves.length; i++) {
    const path = pathOf(leaves[i])
    let j = 0
    while (j < path.length && j < winnerPath.length && path[j] === winnerPath[j]) j++
    const a = path[j] || path[path.length - 1]
    const b = winnerPath[j] || winnerPath[winnerPath.length - 1]
    if (cmpWrite(a, b) > 0) {
      winner = leaves[i]
      winnerPath = path
    }
  }
  const deletion = writes.find(w => w.op === 'delete' && w.id === winner.id)
  return {
    winner: deletion || winner,
    strategy: leaves.length > 1 ? 'highest-client-id-wins' : 'last-write-wins',
    deterministic: true
  }
}

/**
 * @param {MapWriteGroup} group
 * @param {Array<MapWrite>} writes
 * @return {MapConflict|null}
 */
const createConflict = (group, writes) => {
  const setCount = writes.reduce((n, w) => w.op === 'set' ? n + 1 : n, 0)
  if (setCount === 0 || (setCount === 1 && writes.length === 1)) {
    return null
  }
  /**
   * @type {MapConflictKind}
   */
  const kind = setCount === writes.length ? 'set-set' : 'delete-set'
  const ambiguous = writes.some(w => w.ambiguous)
  const sources = new Set(writes.map(w => w.source))
  const source = sources.size === 1 ? writes[0].source : 'mixed'
  const resolution = resolveWrites(writes)
  const message = `${ambiguous ? 'Ambiguous ' : ''}${kind} conflict on key "${group.key}" of "${group.parentId}" (${source}): ${writes.map(w => w.snapshot.summary).join(', ')}; resolved to ${resolution.winner.snapshot.summary}`
  return {
    type: ambiguous ? 'ambiguous' : kind,
    kind,
    ambiguous,
    key: group.key,
    parentId: group.parentId,
    source,
    message,
    writes,
    resolution
  }
}

/**
 * @param {Transaction} transaction
 * @return {Map<string,MapWriteGroup>}
 */
const getTransactionWrites = transaction => transaction._mapWrites || (transaction._mapWrites = new Map())

/**
 * @param {Map<string,MapWriteGroup>} groups
 * @param {string} parentId
 * @param {string} key
 * @return {MapWriteGroup}
 */
const getGroup = (groups, parentId, key) => {
  const k = groupKey(parentId, key)
  let group = groups.get(k)
  if (group === undefined) {
    group = { parentId, key, writes: [] }
    groups.set(k, group)
  }
  return group
}

/**
 * Called before a local map write is performed. Throws in error mode if the write would conflict
 * with a previous write in this transaction.
 *
 * @param {Transaction} transaction
 * @param {YType} parent
 * @param {string} key
 * @param {'set'|'delete'} op
 * @param {ID} id id of the created item (set) or the deleted item (delete)
 * @param {Item|null} left the current entry of the key
 * @param {AbstractContent} content
 */
export const recordLocalMapWrite = (transaction, parent, key, op, id, left, content) => {
  const group = getGroup(getTransactionWrites(transaction), parentIdOfType(parent), key)
  const write = createWrite(op, 'local', id, op === 'set' && left !== null ? idToString(left.lastId) : null, key, describeContent(content, op === 'set' ? 0 : content.getLength() - 1))
  if (transaction.doc.mapConflictPolicy === 'error' && group.writes.length > 0) {
    const conflict = createConflict(group, group.writes.concat(write))
    if (conflict !== null) {
      throw new MapConflictError([conflict])
    }
  }
  group.writes.push(write)
}

/**
 * @param {StructStore} store
 * @param {number} client
 * @param {number} clock
 * @return {Item|null} The integrated item at this position, or null if it is unknown or a GC
 */
const getStoreItem = (store, client, clock) => {
  const structs = store.clients.get(client)
  if (structs === undefined || clock >= getState(store, client) || store.skips.hasId(createID(client, clock))) {
    return null
  }
  const struct = structs[findIndexSS(structs, clock)]
  return struct instanceof Item ? struct : null
}

/**
 * @param {Array<Item>} items sorted by clock
 * @param {number} clock
 * @return {number} index of the first item that ends after clock
 */
const lowerBound = (items, clock) => {
  let lo = 0
  let hi = items.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    const item = items[mid]
    if (item.id.clock + item.length <= clock) {
      lo = mid + 1
    } else {
      hi = mid
    }
  }
  return lo
}

/**
 * Decode an update and extract all map writes that are not yet known to ydoc.
 *
 * @param {Doc} ydoc
 * @param {Uint8Array} update
 * @param {typeof UpdateDecoderV1 | typeof UpdateDecoderV2} YDecoder
 * @return {Map<string,MapWriteGroup>}
 */
const readRemoteMapWrites = (ydoc, update, YDecoder) => {
  const store = ydoc.store
  const { structs, ds } = decodeUpdateV2(update, YDecoder)
  /**
   * @type {Map<number,Array<Item>>}
   */
  const itemsByClient = new Map()
  structs.forEach(struct => {
    if (struct instanceof Item) {
      let items = itemsByClient.get(struct.id.client)
      if (items === undefined) {
        items = []
        itemsByClient.set(struct.id.client, items)
      }
      items.push(struct)
    }
  })
  /**
   * @param {ID} id
   * @return {Item|null}
   */
  const findUpdateItem = id => {
    const items = itemsByClient.get(id.client)
    if (items === undefined) return null
    const item = items[lowerBound(items, id.clock)]
    return item !== undefined && item.id.clock <= id.clock ? item : null
  }
  /**
   * @type {Map<Item,{parentId:string,key:string}|null>}
   */
  const infos = new Map()
  /**
   * Resolve parent & key of an update item. Items that have an origin don't encode parent info,
   * it is inherited from the referenced item.
   *
   * @param {Item} item
   * @return {{parentId:string,key:string}|null}
   */
  const resolveInfo = item => {
    /**
     * @type {Array<Item>}
     */
    const chain = []
    const visited = new Set()
    /**
     * @type {{parentId:string,key:string}|null}
     */
    let info = null
    /**
     * @type {Item|null}
     */
    let curr = item
    while (curr !== null) {
      if (infos.has(curr)) {
        info = /** @type {any} */ (infos.get(curr))
        break
      }
      if (visited.has(curr)) break
      visited.add(curr)
      chain.push(curr)
      if (curr.parent !== null) {
        info = curr.parentSub === null
          ? null
          : { parentId: typeof curr.parent === 'string' ? curr.parent : idToString(/** @type {ID} */ (curr.parent)), key: curr.parentSub }
        break
      }
      const ref = curr.origin || curr.rightOrigin
      if (ref === null) break
      const storeItem = getStoreItem(store, ref.client, ref.clock)
      if (storeItem !== null) {
        info = storeItem.parentSub === null || !(storeItem.parent instanceof YType)
          ? null
          : { parentId: parentIdOfType(storeItem.parent), key: storeItem.parentSub }
        break
      }
      curr = findUpdateItem(ref)
    }
    chain.forEach(c => infos.set(c, info))
    return info
  }
  /**
   * @type {Map<string,MapWriteGroup>}
   */
  const groups = new Map()
  /**
   * Ids of entries that are overwritten by a set in this update. Their deletion is implied by the
   * overwrite and is not an explicit delete.
   *
   * @type {Set<string>}
   */
  const overwritten = new Set()
  itemsByClient.forEach(items => {
    items.forEach(item => {
      const info = resolveInfo(item)
      if (info === null) return
      const group = getGroup(groups, info.parentId, info.key)
      for (let i = 0; i < item.length; i++) {
        const clock = item.id.clock + i
        const origin = i === 0 ? item.origin : createID(item.id.client, clock - 1)
        if (origin !== null) overwritten.add(idToString(origin))
        if (getStoreItem(store, item.id.client, clock) !== null) continue
        group.writes.push(createWrite('set', 'remote', createID(item.id.client, clock), origin && idToString(origin), info.key, describeContent(item.content, i)))
      }
    })
  })
  ds.clients.forEach((ranges, client) => {
    const storeStructs = store.clients.get(client)
    const updateItems = itemsByClient.get(client) || []
    ranges.getIds().forEach(({ clock, len }) => {
      const end = clock + len
      /**
       * @param {Item} item
       * @param {{parentId:string,key:string}|null} info
       * @param {boolean} fromStore
       */
      const addDeletes = (item, info, fromStore) => {
        if (info === null || (fromStore && item.deleted)) return
        const from = Math.max(clock, item.id.clock)
        const to = Math.min(end, item.id.clock + item.length)
        for (let c = from; c < to; c++) {
          const id = createID(client, c)
          if (overwritten.has(idToString(id)) || (!fromStore && getStoreItem(store, client, c) !== null)) continue
          getGroup(groups, info.parentId, info.key).writes.push(createWrite('delete', 'remote', id, null, info.key, describeContent(item.content, c - item.id.clock)))
        }
      }
      if (storeStructs !== undefined && clock < getState(store, client)) {
        for (let si = findIndexSS(storeStructs, clock); si < storeStructs.length && storeStructs[si].id.clock < end; si++) {
          const struct = storeStructs[si]
          if (struct instanceof Item && struct.parentSub !== null && struct.parent instanceof YType) {
            addDeletes(struct, { parentId: parentIdOfType(struct.parent), key: struct.parentSub }, true)
          }
        }
      }
      for (let ui = lowerBound(updateItems, clock); ui < updateItems.length && updateItems[ui].id.clock < end; ui++) {
        addDeletes(updateItems[ui], resolveInfo(updateItems[ui]), false)
      }
    })
  })
  return groups
}

/**
 * Detect map conflicts in an update before it is applied. In error mode, this throws a
 * MapConflictError before anything of the update is integrated.
 *
 * @param {Doc} ydoc
 * @param {Uint8Array} update
 * @param {typeof UpdateDecoderV1 | typeof UpdateDecoderV2} YDecoder
 * @return {Map<string,MapWriteGroup>} The new remote writes, to be added to the applying transaction
 */
export const checkUpdateMapConflicts = (ydoc, update, YDecoder) => {
  const remoteGroups = readRemoteMapWrites(ydoc, update, YDecoder)
  if (ydoc.mapConflictPolicy === 'error') {
    const existing = ydoc._transaction?._mapWrites
    /**
     * @type {Array<MapConflict>}
     */
    const conflicts = []
    remoteGroups.forEach((group, k) => {
      const prev = existing?.get(k)
      const conflict = createConflict(group, mergeWrites(prev ? prev.writes : [], group.writes))
      if (conflict !== null) conflicts.push(conflict)
    })
    if (conflicts.length > 0) {
      throw new MapConflictError(conflicts)
    }
  }
  return remoteGroups
}

/**
 * @param {Array<MapWrite>} writes
 * @param {Array<MapWrite>} newWrites
 */
const mergeWrites = (writes, newWrites) => {
  const known = new Set(writes.map(w => w.op + w.id))
  return writes.concat(newWrites.filter(w => !known.has(w.op + w.id)))
}

/**
 * @param {Transaction} transaction
 * @param {Map<string,MapWriteGroup>} remoteGroups
 */
export const addRemoteMapWrites = (transaction, remoteGroups) => {
  const groups = getTransactionWrites(transaction)
  remoteGroups.forEach(remote => {
    if (remote.writes.length === 0) return
    const group = getGroup(groups, remote.parentId, remote.key)
    group.writes = mergeWrites(group.writes, remote.writes)
  })
}

/**
 * Record the conflicts of a finished transaction (collect mode).
 *
 * @param {Transaction} transaction
 */
export const finalizeTransactionMapConflicts = transaction => {
  const groups = transaction._mapWrites
  const doc = transaction.doc
  if (groups === null || doc.mapConflictPolicy !== 'collect') return
  groups.forEach(group => {
    const conflict = createConflict(group, group.writes)
    if (conflict !== null) {
      doc._mapConflicts.push(conflict)
    }
  })
}

/**
 * @param {Object<string,number>} obj
 * @param {string} key
 */
const increment = (obj, key) => {
  const prev = Object.prototype.hasOwnProperty.call(obj, key) ? obj[key] : 0
  Object.defineProperty(obj, key, { value: prev + 1, enumerable: true, writable: true, configurable: true })
}

/**
 * @param {Array<MapConflict>} conflicts
 * @return {MapConflictSummary}
 */
export const summarizeMapConflicts = conflicts => {
  /**
   * @type {MapConflictSummary}
   */
  const summary = { count: conflicts.length, total: conflicts.length, ambiguous: 0, byType: {}, byKey: {}, byParent: {}, bySource: {} }
  conflicts.forEach(c => {
    if (c.ambiguous) summary.ambiguous++
    increment(summary.byType, c.type)
    increment(summary.byKey, c.key)
    increment(summary.byParent, c.parentId)
    increment(summary.bySource, c.source)
  })
  return summary
}
