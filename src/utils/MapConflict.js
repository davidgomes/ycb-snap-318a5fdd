/**
 * Deterministic conflict tracking for Y.Map-style attribute writes.
 *
 * Policies:
 * - allow: do not record or reject conflicts
 * - collect: record conflicts and keep the Yjs resolution
 * - error: reject the whole transaction so nothing is partially applied
 */

/**
 * @typedef {import('../structs/Item.js').Item} Item
 * @typedef {import('../ytype.js').YType} YType
 * @typedef {import('./Doc.js').Doc} Doc
 * @typedef {import('./Transaction.js').Transaction} Transaction
 */

/**
 * @typedef {Object} MapConflictWrite
 * @property {'set'|'delete'} kind
 * @property {number|null} client
 * @property {number|null} clock
 * @property {{ summary: string }} snapshot
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId
 * @property {'set-set'|'delete-set'|'ambiguous'} type
 * @property {boolean} ambiguous
 * @property {'local'|'remote'|'mixed'} source
 * @property {string} message
 * @property {Array<MapConflictWrite>} writes
 * @property {{ winner: string, strategy: string, deterministic: boolean }} resolution
 */

/**
 * @typedef {Object} MapConflictGroup
 * @property {YType} parent
 * @property {string} key
 * @property {Array<Item>} inserts
 * @property {Array<Item|null>} deletes
 */

/**
 * @typedef {Object} DocRollback
 * @property {Array<{ item: Item, left: Item|null, right: Item|null, info: number, parent: any, parentSub: string|null, length: number, redone: import('./ID.js').ID|null, contentStr: string|undefined, contentArr: Array<any>|undefined }>} items
 * @property {Array<{ type: YType, start: Item|null, map: Map<string, Item>, length: number, item: Item|null, doc: Doc|null, hasFormatting: boolean, prelim: any, searchMarker: Array<any>|null }>} types
 * @property {Map<number, Array<any>>} structLists
 * @property {Array<[Doc, Item|null]>} subdocs
 * @property {Set<Doc>} subdocSet
 * @property {Map<string, YType>} share
 * @property {number} clientID
 * @property {any} pendingStructs
 * @property {any} pendingDs
 */

/**
 * @param {any} struct
 * @return {struct is Item}
 */
const isItemStruct = struct => struct != null && struct.content != null && struct.id != null && struct.left !== undefined

const POLICIES = new Set(['allow', 'collect', 'error'])

/**
 * @param {string} policy
 * @return {'allow'|'collect'|'error'}
 */
export const normalizeMapConflictPolicy = policy => {
  if (policy == null) return 'allow'
  if (!POLICIES.has(policy)) {
    throw new Error(`Unknown mapConflictPolicy "${policy}". Expected 'allow', 'collect', or 'error'.`)
  }
  return /** @type {'allow'|'collect'|'error'} */ (policy)
}

export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(conflicts.map(conflict => conflict.message).join('\n') || 'Map conflict')
    this.name = 'MapConflictError'
    /**
     * @type {Array<MapConflict>}
     */
    this.conflicts = conflicts
  }
}

/**
 * @param {Transaction} transaction
 * @param {YType} parent
 * @param {string} key
 * @return {MapConflictGroup}
 */
const groupFor = (transaction, parent, key) => {
  if (transaction._mapGroups == null) {
    /**
     * @type {Map<YType, Map<string, MapConflictGroup>>}
     */
    transaction._mapGroups = new Map()
  }
  let byKey = transaction._mapGroups.get(parent)
  if (byKey == null) {
    byKey = new Map()
    transaction._mapGroups.set(parent, byKey)
  }
  let group = byKey.get(key)
  if (group == null) {
    group = { parent, key, inserts: [], deletes: [] }
    byKey.set(key, group)
  }
  return group
}

/**
 * @param {Transaction} transaction
 * @param {Item} item
 */
export const noteMapInsert = (transaction, item) => {
  if (transaction.doc.mapConflictPolicy === 'allow') return
  const parent = /** @type {YType} */ (item.parent)
  if (parent == null || parent._map == null || item.parentSub == null) return
  const group = groupFor(transaction, parent, item.parentSub)
  if (!group.inserts.includes(item)) group.inserts.push(item)
}

/**
 * Record an explicit map delete (user delete or a delete from an update's delete set).
 * Overwrite deletes performed while integrating a replacement value are not recorded here.
 *
 * @param {Transaction} transaction
 * @param {YType} parent
 * @param {string} key
 * @param {Item|null} item
 */
export const noteMapDelete = (transaction, parent, key, item) => {
  if (transaction.doc.mapConflictPolicy === 'allow') return
  if (parent == null || parent._map == null) return
  const group = groupFor(transaction, parent, key)
  if (item == null) {
    if (!group.deletes.includes(null)) group.deletes.push(null)
    return
  }
  if (!group.deletes.includes(item)) group.deletes.push(item)
}

/**
 * @param {YType} parent
 * @param {string} key
 * @return {Array<Item>}
 */
const chainOf = (parent, key) => {
  const rightmost = parent._map.get(key)
  if (rightmost == null) return []
  let item = rightmost
  while (item.left != null) item = item.left
  /**
   * @type {Array<Item>}
   */
  const chain = []
  while (item != null) {
    chain.push(item)
    item = /** @type {Item} */ (item.right)
  }
  return chain
}

/**
 * @param {Item} item
 * @return {string}
 */
const originKey = item => {
  if (item.origin == null) return 'root'
  return item.origin.client + ':' + item.origin.clock
}

/**
 * @param {YType} parent
 * @return {string}
 */
const parentIdOf = parent => {
  if (parent._item == null) {
    const doc = parent.doc
    if (doc != null) {
      for (const [key, value] of doc.share.entries()) {
        if (value === parent) return key
      }
    }
    return ''
  }
  return parent._item.id.client + ':' + parent._item.id.clock
}

/**
 * @param {any} content
 * @return {boolean}
 */
const isAmbiguousContent = content => {
  if (content == null) return false
  if (content.type != null && content.type._map != null && content.type.doc !== undefined) return true
  if (content.doc != null && content.doc.store != null && content.doc.guid != null && typeof content.doc.get === 'function') return true
  return false
}

/**
 * @param {any} content
 * @return {string}
 */
const summarizeContent = content => {
  if (content == null) return 'empty'
  if (content.type != null && content.type._map != null) return 'Y.Type'
  if (content.doc != null && content.doc.guid != null && content.doc.store != null) return 'subdoc:' + content.doc.guid
  if (typeof content.str === 'string') return JSON.stringify(content.str)
  if (content.arr != null) {
    try {
      const value = content.arr.length === 1 ? content.arr[0] : content.arr
      const encoded = JSON.stringify(value)
      return encoded == null ? 'json' : encoded
    } catch (e) {
      return 'json'
    }
  }
  if (content.content instanceof Uint8Array) return 'binary:' + content.content.byteLength
  if (content.embed !== undefined) {
    try {
      return 'embed:' + JSON.stringify(content.embed)
    } catch (e) {
      return 'embed'
    }
  }
  return 'value'
}

/**
 * @param {'set'|'delete'} kind
 * @param {Item|null} item
 * @return {MapConflictWrite}
 */
const toWrite = (kind, item) => {
  const body = item == null ? '<missing>' : summarizeContent(item.content)
  const summary = kind + ':' + body
  return {
    kind,
    client: item == null ? null : item.id.client,
    clock: item == null ? null : item.id.clock,
    snapshot: { summary: summary.length > 0 ? summary : kind }
  }
}

/**
 * @param {Transaction} transaction
 * @param {Array<Item|null>} items
 * @return {'local'|'remote'|'mixed'}
 */
const conflictSource = (transaction, items) => {
  const docClient = transaction.doc.clientID
  let sawLocal = false
  let sawRemote = false
  for (const item of items) {
    if (item == null) continue
    if (item.id.client === docClient) sawLocal = true
    else sawRemote = true
  }
  if (transaction.local) {
    return sawRemote ? 'mixed' : 'local'
  }
  if (sawLocal && sawRemote) return 'mixed'
  if (sawLocal && !sawRemote) return 'local'
  return 'remote'
}

/**
 * @param {Array<Item>} chain
 * @return {string}
 */
const winnerOf = chain => {
  /** @type {Item|null} */
  let winner = null
  for (const item of chain) {
    if (!item.deleted) winner = item
  }
  if (winner == null) return 'delete'
  return winner.id.client + ':' + winner.id.clock
}

/**
 * @param {Transaction} transaction
 * @param {MapConflictGroup} group
 * @param {'set-set'|'delete-set'} kind
 * @param {Array<Item>} setItems
 * @param {Array<Item|null>} deleteItems
 * @return {MapConflict}
 */
const makeConflict = (transaction, group, kind, setItems, deleteItems) => {
  const chain = chainOf(group.parent, group.key)
  const writes = [
    ...deleteItems.map(item => toWrite('delete', item)),
    ...setItems.map(item => toWrite('set', item))
  ]
  const ambiguous = [...deleteItems, ...setItems].some(item => item != null && isAmbiguousContent(item.content))
  const type = ambiguous ? 'ambiguous' : kind
  const parentId = parentIdOf(group.parent)
  const source = conflictSource(transaction, [...deleteItems, ...setItems])
  const message = `Map ${type} conflict on key "${group.key}" in parent "${parentId}" (${source}).`
  return {
    key: group.key,
    parentId,
    type,
    ambiguous,
    source,
    message,
    writes,
    resolution: {
      winner: winnerOf(chain.length > 0 ? chain : setItems),
      strategy: 'higher-client-id',
      deterministic: true
    }
  }
}

/**
 * @param {Transaction} transaction
 * @return {Array<MapConflict>}
 */
export const finalizeMapConflicts = transaction => {
  if (transaction.doc.mapConflictPolicy === 'allow' || transaction._mapGroups == null) return []
  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  transaction._mapGroups.forEach(byKey => {
    byKey.forEach(group => {
      const chain = chainOf(group.parent, group.key)
      /** @type {Array<Item>|null} */
      let siblingItems = null
      /** @type {Map<string, Array<Item>>} */
      const byOrigin = new Map()
      for (const item of chain) {
        const key = originKey(item)
        const list = byOrigin.get(key)
        if (list == null) byOrigin.set(key, [item])
        else list.push(item)
      }
      for (const items of byOrigin.values()) {
        if (items.length >= 2 && items.some(item => group.inserts.includes(item))) {
          siblingItems = items
          break
        }
      }
      const localMultiSet = transaction.local && group.inserts.length >= 2
      const crossClientDelete = group.deletes.some(deleted =>
        deleted != null && group.inserts.some(inserted => inserted !== deleted && inserted.id.client !== deleted.id.client)
      )
      const localDeleteSet = transaction.local && group.deletes.length > 0 && group.inserts.length > 0
      if (siblingItems != null || localMultiSet) {
        const setItems = siblingItems != null ? siblingItems : group.inserts.slice()
        conflicts.push(makeConflict(transaction, group, 'set-set', setItems, []))
      }
      if ((localDeleteSet || crossClientDelete) && siblingItems == null) {
        conflicts.push(makeConflict(transaction, group, 'delete-set', group.inserts.slice(), group.deletes.slice()))
      }
    })
  })
  return conflicts
}

/**
 * @param {Doc} doc
 * @return {DocRollback}
 */
export const snapshotDoc = doc => {
  /**
   * @type {DocRollback['items']}
   */
  const items = []
  doc.store.clients.forEach(structs => {
    for (const struct of structs) {
      if (!isItemStruct(struct)) continue
      const content = /** @type {any} */ (struct.content)
      items.push({
        item: struct,
        left: struct.left,
        right: struct.right,
        info: struct.info,
        parent: struct.parent,
        parentSub: struct.parentSub,
        length: struct.length,
        redone: struct.redone,
        contentStr: typeof content.str === 'string' ? content.str : undefined,
        contentArr: content.arr != null ? content.arr.slice() : undefined
      })
    }
  })
  /**
   * @type {DocRollback['types']}
   */
  const types = []
  const seen = new Set()
  /**
   * @param {YType|null|undefined} type
   */
  const visit = type => {
    if (type == null || type._map == null || seen.has(type)) return
    seen.add(type)
    types.push({
      type,
      start: type._start,
      map: new Map(type._map),
      length: type._length,
      item: type._item,
      doc: type.doc,
      hasFormatting: type._hasFormatting,
      prelim: type._prelim,
      searchMarker: type._searchMarker ? type._searchMarker.slice() : null
    })
    /** @type {Item|null} */
    let child = type._start
    while (child != null) {
      const content = /** @type {any} */ (child.content)
      if (content != null && content.type != null) visit(content.type)
      child = child.right
    }
    type._map.forEach(start => {
      /** @type {Item|null} */
      let item = start
      while (item != null && item.left != null) item = item.left
      while (item != null) {
        const content = /** @type {any} */ (item.content)
        if (content != null && content.type != null) visit(content.type)
        item = item.right
      }
    })
  }
  doc.share.forEach(type => visit(type))
  /** @type {Map<number, Array<any>>} */
  const structLists = new Map()
  doc.store.clients.forEach((structs, client) => {
    structLists.set(client, structs.slice())
  })
  /** @type {Array<[Doc, Item|null]>} */
  const subdocs = []
  doc.subdocs.forEach(subdoc => {
    subdocs.push([subdoc, subdoc._item])
  })
  return {
    items,
    types,
    structLists,
    subdocs,
    subdocSet: new Set(doc.subdocs),
    share: new Map(doc.share),
    clientID: doc.clientID,
    pendingStructs: doc.store.pendingStructs,
    pendingDs: doc.store.pendingDs
  }
}

/**
 * Restore a document to the state captured by {@link snapshotDoc}.
 * Used so error-policy transactions do not leave a partial update behind.
 *
 * @param {Doc} doc
 * @param {DocRollback|null|undefined} snap
 */
export const restoreDoc = (doc, snap) => {
  if (snap == null) return
  for (const saved of snap.items) {
    saved.item.left = saved.left
    saved.item.right = saved.right
    saved.item.info = saved.info
    saved.item.parent = saved.parent
    saved.item.parentSub = saved.parentSub
    saved.item.length = saved.length
    saved.item.redone = saved.redone
    const content = /** @type {any} */ (saved.item.content)
    if (saved.contentStr !== undefined) content.str = saved.contentStr
    if (saved.contentArr !== undefined) content.arr = saved.contentArr.slice()
  }
  for (const saved of snap.types) {
    saved.type._start = saved.start
    saved.type._map = new Map(saved.map)
    saved.type._length = saved.length
    saved.type._item = saved.item
    saved.type.doc = saved.doc
    saved.type._hasFormatting = saved.hasFormatting
    saved.type._prelim = saved.prelim
    if (saved.type._searchMarker != null) {
      saved.type._searchMarker.length = 0
      if (saved.searchMarker != null) {
        for (const marker of saved.searchMarker) saved.type._searchMarker.push(marker)
      }
    } else {
      saved.type._searchMarker = saved.searchMarker
    }
  }
  doc.store.clients.clear()
  snap.structLists.forEach((list, client) => {
    doc.store.clients.set(client, list.slice())
  })
  doc.store.pendingStructs = snap.pendingStructs
  doc.store.pendingDs = snap.pendingDs
  doc.share.clear()
  snap.share.forEach((type, key) => {
    doc.share.set(key, type)
  })
  doc.clientID = snap.clientID
  if (doc._transaction != null) {
    doc._transaction.subdocsAdded.forEach(subdoc => {
      if (!snap.subdocSet.has(subdoc)) subdoc._item = null
    })
  }
  doc.subdocs.clear()
  for (const [subdoc, item] of snap.subdocs) {
    subdoc._item = item
    doc.subdocs.add(subdoc)
  }
}

/**
 * @param {Array<MapConflict>} conflicts
 * @return {{ byType: Object<string, number>, byKey: Object<string, number>, byParent: Object<string, number>, bySource: Object<string, number>, count: number, total: number }}
 */
export const summarizeMapConflicts = conflicts => {
  /** @type {Object<string, number>} */
  const byType = {}
  /** @type {Object<string, number>} */
  const byKey = {}
  /** @type {Object<string, number>} */
  const byParent = {}
  /** @type {Object<string, number>} */
  const bySource = {}
  for (const conflict of conflicts) {
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
