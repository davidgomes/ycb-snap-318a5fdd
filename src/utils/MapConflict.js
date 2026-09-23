/**
 * Strict, deterministic detection of overlapping Y.Map key writes.
 *
 * `allow` keeps the historical last-write-wins behavior.
 * `collect` records conflicts and still applies the update.
 * `error` throws {@link MapConflictError} before a conflicting update is visible.
 */

import {
  applyUpdate,
  encodeStateAsUpdate
} from './encoding.js'

/**
 * @typedef {'allow'|'collect'|'error'} MapConflictPolicy
 */

/**
 * @typedef {'set'|'delete'} MapWriteOp
 */

/**
 * @typedef {'local'|'remote'|'mixed'} MapConflictSource
 */

/**
 * @typedef {'set-set'|'delete-set'} MapConflictKind
 */

/**
 * @typedef {Object} MapWrite
 * @property {MapWriteOp} op
 * @property {string} key
 * @property {any} value
 * @property {number} client
 * @property {number} clock
 * @property {string} summary
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId
 * @property {string} type
 * @property {MapConflictKind} kind
 * @property {boolean} ambiguous
 * @property {MapConflictSource} source
 * @property {string} message
 * @property {Array<{ op: MapWriteOp, key: string, client: number, clock: number, value: any, snapshot: { summary: string } }>} writes
 * @property {{ winner: any, strategy: string, deterministic: boolean }} resolution
 */

const POLICIES = ['allow', 'collect', 'error']

/**
 * @param {string} policy
 * @return {policy is MapConflictPolicy}
 */
export const isMapConflictPolicy = policy => POLICIES.includes(policy)

/**
 * Thrown when `mapConflictPolicy` is `error` and overlapping map writes are found.
 * `conflicts` lists every conflict that blocked the transaction or merged update.
 */
export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    const first = conflicts[0]
    super(first ? first.message : 'Map conflict')
    this.name = 'MapConflictError'
    /**
     * @type {Array<MapConflict>}
     */
    this.conflicts = conflicts
  }
}

/**
 * @param {any} value
 * @return {boolean}
 */
const isYType = value => value != null && typeof value.setAttr === 'function' && value._map instanceof Map && typeof value.toDelta === 'function'

/**
 * @param {any} value
 * @return {boolean}
 */
const isSubdoc = value => value != null && typeof value === 'object' && typeof value.guid === 'string' && value.share instanceof Map && value.store != null && typeof value.transact === 'function' && !isYType(value)

/**
 * @param {any} value
 * @return {boolean}
 */
const isStructuredValue = value => isYType(value) || isSubdoc(value)

/**
 * @param {any} value
 * @return {string}
 */
const summarizeValue = value => {
  if (isYType(value)) return '[YType]'
  if (isSubdoc(value)) return '[Subdoc ' + value.guid + ']'
  if (typeof value === 'string') return JSON.stringify(value)
  if (typeof value === 'bigint') return value.toString()
  if (typeof value === 'symbol') return value.toString()
  if (value instanceof Uint8Array) return '[Binary ' + value.byteLength + ']'
  if (value === undefined) return 'undefined'
  try {
    const json = JSON.stringify(value)
    if (typeof json === 'string' && json.length > 0) return json
  } catch (e) {
    return e instanceof Error ? 'value' : 'value'
  }
  const text = String(value)
  return text.length > 0 ? text : 'value'
}

/**
 * @param {MapWriteOp} op
 * @param {any} value
 * @return {string}
 */
const summaryFor = (op, value) => {
  if (op === 'delete') {
    const rendered = value === undefined ? '' : ' ' + summarizeValue(value)
    const summary = 'delete' + rendered
    return summary.length > 0 ? summary : 'delete'
  }
  const summary = 'set ' + summarizeValue(value)
  return summary.length > 0 ? summary : 'set'
}

/**
 * @param {{ client: number, clock: number }} id
 * @return {string}
 */
const idKey = id => id.client + ':' + id.clock

/**
 * @param {import('./Doc.js').Doc} doc
 * @param {import('./ID.js').ID} id
 * @return {any}
 */
const tryGetItem = (doc, id) => {
  if (id == null) return null
  const structs = doc.store.clients.get(id.client)
  if (structs == null || structs.length === 0) return null
  const last = structs[structs.length - 1]
  if (id.clock < structs[0].id.clock || id.clock >= last.id.clock + last.length) return null
  let left = 0
  let right = structs.length - 1
  while (left <= right) {
    const midindex = Math.floor((left + right) / 2)
    const mid = structs[midindex]
    if (mid.id.clock <= id.clock) {
      if (id.clock < mid.id.clock + mid.length) return mid
      left = midindex + 1
    } else {
      right = midindex - 1
    }
  }
  return null
}

/**
 * @param {any} parent
 * @return {string}
 */
const parentIdOf = parent => {
  if (parent == null) return 'unknown'
  if (typeof parent === 'string') return parent
  if (typeof parent.client === 'number' && typeof parent.clock === 'number' && parent.content == null && parent._map == null) {
    return idKey(parent)
  }
  if (parent._item && parent._item.id) return idKey(parent._item.id)
  const doc = parent.doc
  if (doc && doc.share) {
    for (const [key, value] of doc.share) {
      if (value === parent) return key
    }
  }
  return 'unknown'
}

/**
 * @param {any} item
 * @param {import('./Doc.js').Doc} doc
 * @return {{ key: string, parentId: string, parentType: any } | null}
 */
const locationFromItemParent = (item, doc) => {
  if (item.parentSub == null) return null
  const parent = item.parent
  if (typeof parent === 'string') {
    return { key: item.parentSub, parentId: parent, parentType: doc.share.get(parent) || null }
  }
  if (parent && typeof parent.client === 'number' && typeof parent.clock === 'number' && parent._map == null && parent.content == null) {
    const parentItem = tryGetItem(doc, parent)
    const parentType = parentItem && parentItem.content && parentItem.content.type && isYType(parentItem.content.type) ? parentItem.content.type : null
    return { key: item.parentSub, parentId: idKey(parent), parentType }
  }
  if (isYType(parent)) {
    return { key: item.parentSub, parentId: parentIdOf(parent), parentType: parent }
  }
  return null
}

/**
 * @param {Array<MapWrite>} writes
 * @param {import('./Doc.js').Doc} doc
 * @param {MapConflictSource | null} forcedSource
 * @return {MapConflictSource}
 */
const sourceOf = (writes, doc, forcedSource) => {
  if (forcedSource) return forcedSource
  let hasLocal = false
  let hasRemote = false
  for (let i = 0; i < writes.length; i++) {
    if (writes[i].client === doc.clientID) hasLocal = true
    else hasRemote = true
  }
  if (hasLocal && hasRemote) return 'mixed'
  if (hasLocal) return 'local'
  return 'remote'
}

/**
 * @param {import('./Doc.js').Doc} doc
 * @param {string} key
 * @param {string} parentId
 * @param {MapConflictKind} kind
 * @param {Array<MapWrite>} writes
 * @param {any} winner
 * @param {string} strategy
 * @param {MapConflictSource | null} forcedSource
 * @return {MapConflict}
 */
const buildConflict = (doc, key, parentId, kind, writes, winner, strategy, forcedSource) => {
  const ambiguous = writes.some(write => isStructuredValue(write.value))
  const type = ambiguous ? 'ambiguous' : kind
  const source = sourceOf(writes, doc, forcedSource)
  const renderedWrites = writes.map(write => ({
    op: write.op,
    key,
    client: write.client,
    clock: write.clock,
    value: write.value,
    snapshot: { summary: write.summary && write.summary.length > 0 ? write.summary : summaryFor(write.op, write.value) }
  }))
  const label = ambiguous ? 'ambiguous ' + kind : kind
  return {
    key,
    parentId,
    type,
    kind,
    ambiguous,
    source,
    message: 'Map conflict (' + label + ') on key "' + key + '" in parent "' + parentId + '" (' + source + ')',
    writes: renderedWrites,
    resolution: {
      winner,
      strategy,
      deterministic: !ambiguous
    }
  }
}

/**
 * @param {MapWrite} write
 * @return {boolean}
 */
const isDeleteWrite = write => write.op === 'delete'

/**
 * Record a user-level map set or delete. Automatic overwrites performed while
 * integrating a single set are not recorded.
 *
 * @param {import('./Transaction.js').Transaction} transaction
 * @param {any} parent
 * @param {string} key
 * @param {MapWriteOp} op
 * @param {any} value
 * @param {number} client
 * @param {number} clock
 */
export const noteMapWrite = (transaction, parent, key, op, value, client, clock) => {
  const doc = transaction.doc
  const policy = doc.mapConflictPolicy || 'allow'
  if (policy === 'allow' || doc._suspendMapConflicts) return
  if (transaction._mapWrites == null) {
    /**
     * @type {Map<any, Map<string, Array<MapWrite>>>}
     */
    transaction._mapWrites = new Map()
  }
  let byKey = transaction._mapWrites.get(parent)
  if (byKey == null) {
    byKey = new Map()
    transaction._mapWrites.set(parent, byKey)
  }
  let writes = byKey.get(key)
  if (writes == null) {
    writes = []
    byKey.set(key, writes)
  }
  writes.push({
    op,
    key,
    value,
    client,
    clock,
    summary: summaryFor(op, value)
  })
}

/**
 * Conflicts produced by explicit map writes inside one transaction.
 *
 * @param {import('./Transaction.js').Transaction} transaction
 * @return {Array<MapConflict>}
 */
export const collectLocalMapConflicts = transaction => {
  const doc = transaction.doc
  if (transaction._mapWrites == null) return []
  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  transaction._mapWrites.forEach((byKey, parent) => {
    byKey.forEach((writes, key) => {
      const sets = writes.filter(write => write.op === 'set')
      const deletes = writes.filter(isDeleteWrite)
      if (sets.length < 2 && !(sets.length >= 1 && deletes.length >= 1)) return
      const kind = sets.length >= 1 && deletes.length >= 1 ? 'delete-set' : 'set-set'
      const last = writes[writes.length - 1]
      const winner = last.op === 'delete' ? null : last.value
      conflicts.push(buildConflict(doc, key, parentIdOf(parent), kind, writes, winner, 'last-write-wins', 'local'))
    })
  })
  return conflicts
}

/**
 * @param {import('./Doc.js').Doc} doc
 * @param {Array<MapConflict>} conflicts
 */
export const recordMapConflicts = (doc, conflicts) => {
  if (conflicts.length === 0) return
  if (doc._mapConflicts == null) doc._mapConflicts = []
  for (let i = 0; i < conflicts.length; i++) {
    doc._mapConflicts.push(conflicts[i])
  }
}

/**
 * @param {Array<MapConflict>} conflicts
 */
export const summarizeMapConflicts = conflicts => {
  /**
   * @type {Object<string, number>}
   */
  const byType = {}
  /**
   * @type {Object<string, number>}
   */
  const byKey = {}
  /**
   * @type {Object<string, number>}
   */
  const byParent = {}
  /**
   * @type {Object<string, number>}
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
 * @param {any} content
 * @return {boolean}
 */
const contentIsDeleted = content => content != null && content.constructor != null && content.constructor.name === 'ContentDeleted'

/**
 * @param {any} content
 * @return {boolean}
 */
const contentIsAmbiguous = content => {
  if (content == null || content.constructor == null) return false
  const name = content.constructor.name
  return name === 'ContentType' || name === 'ContentDoc'
}

/**
 * @param {any} item
 * @param {import('./IdSet.js').IdSet} deleteSet
 * @return {MapWrite}
 */
const writeFromItem = (item, deleteSet) => {
  const raw = valueOfContent(item.content)
  const deleted = item.deleted === true || contentIsDeleted(item.content) || deleteSet.hasId(item.id)
  const op = deleted ? 'delete' : 'set'
  // Keep structured values on deletes so type/subdoc conflicts stay marked ambiguous.
  const value = deleted && !contentIsAmbiguous(item.content) && !isStructuredValue(raw) ? undefined : raw
  return {
    op,
    key: '',
    value,
    client: item.id.client,
    clock: item.id.clock,
    summary: summaryFor(op, value)
  }
}

/**
 * @param {any} content
 * @return {any}
 */
const valueOfContent = content => {
  if (content == null) return undefined
  const name = content.constructor ? content.constructor.name : ''
  if (name === 'ContentDeleted') return undefined
  if (name === 'ContentString') return content.str
  if (name === 'ContentBinary') return content.content
  if (name === 'ContentEmbed') return content.embed
  if (typeof content.getContent === 'function') {
    const values = content.getContent()
    if (values.length === 0) return undefined
    return values[values.length - 1]
  }
  return undefined
}

/**
 * @param {any} item
 * @return {boolean}
 */
const isItemStruct = item => item != null && item.id != null && item.content != null && Object.prototype.hasOwnProperty.call(item, 'parentSub')

/**
 * Detect overlapping map writes contained in an incoming update, including
 * writes that fork against map entries already integrated in `doc`.
 *
 * @param {import('./Doc.js').Doc} doc
 * @param {{ clients: Map<number, { refs: Array<any> }> }} blockSet
 * @param {import('./IdSet.js').IdSet} deleteSet
 * @return {Array<MapConflict>}
 */
export const detectIncomingMapConflicts = (doc, blockSet, deleteSet) => {
  /**
   * @type {Map<string, any>}
   */
  const updateItems = new Map()
  blockSet.clients.forEach(range => {
    const refs = range.refs
    for (let i = 0; i < refs.length; i++) {
      const struct = refs[i]
      if (!isItemStruct(struct)) continue
      if (tryGetItem(doc, struct.id)) continue
      updateItems.set(idKey(struct.id), struct)
    }
  })
  if (updateItems.size === 0) return []

  /**
   * @param {any} item
   * @param {Set<string>} seen
   * @return {{ key: string, parentId: string, parentType: any } | null}
   */
  const resolveLocation = (item, seen) => {
    const key = idKey(item.id)
    if (seen.has(key)) return null
    seen.add(key)
    const direct = locationFromItemParent(item, doc)
    if (direct) return direct
    if (item.origin == null) return null
    const origin = updateItems.get(idKey(item.origin)) || tryGetItem(doc, item.origin)
    if (!isItemStruct(origin)) return null
    return resolveLocation(origin, seen)
  }

  /**
   * @typedef {{ key: string, parentId: string, parentType: any, updateItems: Array<any> }} ConflictGroup
   */
  /**
   * @type {Map<string, ConflictGroup>}
   */
  const groups = new Map()
  updateItems.forEach(item => {
    const loc = resolveLocation(item, new Set())
    if (loc == null) return
    const groupKey = loc.parentId + '\0' + loc.key
    let group = groups.get(groupKey)
    if (group == null) {
      group = { key: loc.key, parentId: loc.parentId, parentType: loc.parentType, updateItems: [] }
      groups.set(groupKey, group)
    } else if (group.parentType == null && loc.parentType != null) {
      group.parentType = loc.parentType
    }
    group.updateItems.push(item)
  })

  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  groups.forEach(group => {
    /**
     * @type {Map<string, any>}
     */
    const itemsById = new Map()
    for (let i = 0; i < group.updateItems.length; i++) {
      itemsById.set(idKey(group.updateItems[i].id), group.updateItems[i])
    }
    if (group.parentType && group.parentType._map) {
      let current = group.parentType._map.get(group.key) || null
      const guard = new Set()
      while (current && !guard.has(current)) {
        guard.add(current)
        itemsById.set(idKey(current.id), current)
        current = current.left
      }
    }
    /**
     * @type {Map<string, Array<any>>}
     */
    const siblings = new Map()
    itemsById.forEach(item => {
      const origin = item.origin
      const pred = origin && itemsById.has(idKey(origin)) ? idKey(origin) : 'ROOT'
      let list = siblings.get(pred)
      if (list == null) {
        list = []
        siblings.set(pred, list)
      }
      list.push(item)
    })
    /**
     * @type {Map<string, any>}
     */
    const competing = new Map()
    siblings.forEach(list => {
      if (list.length < 2) return
      let involvesUpdate = false
      for (let i = 0; i < list.length; i++) {
        if (updateItems.has(idKey(list[i].id))) involvesUpdate = true
      }
      if (!involvesUpdate) return
      for (let i = 0; i < list.length; i++) competing.set(idKey(list[i].id), list[i])
    })
    if (competing.size === 0) {
      // A set applied onto a key that is already deleted is a delete-set merge.
      for (let i = 0; i < group.updateItems.length; i++) {
        const item = group.updateItems[i]
        if (item.origin == null) continue
        if (updateItems.has(idKey(item.origin))) continue
        const pred = tryGetItem(doc, item.origin)
        if (!isItemStruct(pred) || pred.deleted !== true) continue
        const predLoc = locationFromItemParent(pred, doc) || resolveLocation(pred, new Set())
        if (predLoc == null || predLoc.key !== group.key || predLoc.parentId !== group.parentId) continue
        const incoming = writeFromItem(item, deleteSet)
        if (incoming.op !== 'set') continue
        competing.set(idKey(pred.id), pred)
        competing.set(idKey(item.id), item)
      }
    }
    if (competing.size < 2) return
    /**
     * @type {Array<MapWrite>}
     */
    const writes = []
    competing.forEach(item => {
      const write = writeFromItem(item, deleteSet)
      write.key = group.key
      writes.push(write)
    })
    writes.sort((a, b) => a.client === b.client ? a.clock - b.clock : a.client - b.client)
    const live = writes.filter(write => write.op === 'set')
    const dead = writes.filter(isDeleteWrite)
    if (live.length === 0) return
    const kind = dead.length > 0 ? 'delete-set' : 'set-set'
    const ranked = writes.slice().sort((a, b) => a.client === b.client ? a.clock - b.clock : a.client - b.client)
    const top = ranked[ranked.length - 1]
    const winner = top.op === 'delete' ? null : top.value
    const sameClient = writes.every(write => write.client === writes[0].client)
    conflicts.push(buildConflict(
      doc,
      group.key,
      group.parentId,
      kind,
      writes,
      winner,
      sameClient ? 'last-write-wins' : 'higher-client-id',
      null
    ))
  })
  return conflicts
}

/**
 * @param {import('./Doc.js').Doc} doc
 */
const collectIntegratedTypes = doc => {
  const seen = new Set()
  /**
   * @param {any} type
   */
  const visitType = type => {
    if (!isYType(type) || seen.has(type)) return
    seen.add(type)
    /**
     * @param {any} item
     * @param {'left'|'right'} dir
     */
    const visitChain = (item, dir) => {
      const guard = new Set()
      while (item && !guard.has(item)) {
        guard.add(item)
        const content = item.content
        if (content && isYType(content.type)) visitType(content.type)
        if (content && typeof content.getContent === 'function') {
          const values = content.getContent()
          for (let i = 0; i < values.length; i++) {
            if (isYType(values[i])) visitType(values[i])
          }
        }
        item = item[dir]
      }
    }
    type._map.forEach((/** @type {any} */ item) => visitChain(item, 'left'))
    visitChain(type._start, 'right')
  }
  doc.share.forEach(type => visitType(type))
  return seen
}

/**
 * @param {import('./Doc.js').Doc} doc
 */
export const captureDocSnapshot = doc => {
  /**
   * @type {Map<string, any>}
   */
  const typesByItem = new Map()
  /**
   * @type {Map<string, import('./Doc.js').Doc>}
   */
  const subdocsByGuid = new Map()
  doc.subdocs.forEach(subdoc => {
    subdocsByGuid.set(subdoc.guid, subdoc)
  })
  doc.store.clients.forEach(structs => {
    for (let i = 0; i < structs.length; i++) {
      const struct = /** @type {any} */ (structs[i])
      const content = struct.content
      if (content && isYType(content.type) && struct.id) {
        typesByItem.set(idKey(struct.id), content.type)
      }
      if (content && isSubdoc(content.doc)) {
        subdocsByGuid.set(content.doc.guid, content.doc)
      }
    }
  })
  return {
    update: encodeStateAsUpdate(doc),
    clientID: doc.clientID,
    roots: new Map(doc.share),
    typesByItem,
    subdocsByGuid,
    pendingStructs: doc.store.pendingStructs,
    pendingDs: doc.store.pendingDs
  }
}

/**
 * Roll a document back to a snapshot taken before the current transaction.
 * Type and subdoc object identity for content that existed beforehand is preserved.
 *
 * @param {import('./Doc.js').Doc} doc
 * @param {ReturnType<typeof captureDocSnapshot>} snapshot
 */
export const restoreDocSnapshot = (doc, snapshot) => {
  const types = collectIntegratedTypes(doc)
  snapshot.typesByItem.forEach(type => {
    if (isYType(type)) types.add(type)
  })
  const roots = new Set(snapshot.roots.values())
  types.forEach(type => {
    type._map = new Map()
    type._start = null
    type._length = 0
    type._searchMarker = []
    type._hasFormatting = false
    type._item = null
    type.doc = roots.has(type) ? doc : null
  })
  doc.store.clients = new Map()
  doc.store.skips.clients.clear()
  doc.store.pendingStructs = null
  doc.store.pendingDs = null
  doc.subdocs = new Set()
  doc.share = new Map(snapshot.roots)
  doc._typeRebind = snapshot.typesByItem
  doc._subdocRebind = snapshot.subdocsByGuid
  const prevSuspend = doc._suspendMapConflicts === true
  const prevForce = doc._forceLocalUpdate === true
  const prevSuppress = doc._suppressTransactionEvents === true
  doc._suspendMapConflicts = true
  doc._forceLocalUpdate = true
  doc._suppressTransactionEvents = true
  try {
    applyUpdate(doc, snapshot.update)
  } finally {
    doc._suspendMapConflicts = prevSuspend
    doc._forceLocalUpdate = prevForce
    doc._suppressTransactionEvents = prevSuppress
    doc._typeRebind = null
    doc._subdocRebind = null
  }
  doc.clientID = snapshot.clientID
  doc.store.pendingStructs = snapshot.pendingStructs
  doc.store.pendingDs = snapshot.pendingDs
}
