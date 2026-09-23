/**
 * @module MapConflicts
 */

import {
  Item,
  GC,
  Doc,
  ContentType,
  ContentDoc,
  ContentBinary,
  ContentAny,
  ContentJSON,
  ContentDeleted,
  getState,
  findIndexSS,
  findRootTypeKey,
  ID, Transaction, BlockSet, IdSet // eslint-disable-line
} from '../internals.js'

import { YType } from '../ytype.js'
import * as map from 'lib0/map'

/**
 * - `allow`: map writes are applied as usual, conflicts are neither collected nor rejected.
 * - `collect`: conflicts are recorded and can be inspected via `ydoc.getMapConflicts()`.
 * - `error`: conflicting writes throw a {@link MapConflictError} before they are applied.
 *
 * @typedef {'allow'|'collect'|'error'} MapConflictPolicy
 */

/**
 * @typedef {Object} MapWriteSnapshot
 * @property {string} summary Human readable description of the write.
 * @property {'any'|'binary'|'type'|'doc'|'deleted'|'other'} kind Kind of the written (or removed) content.
 */

/**
 * @typedef {Object} MapWrite
 * @property {'set'|'delete'} op
 * @property {string} key
 * @property {string} parentId
 * @property {'local'|'remote'} source
 * @property {{ client: number, clock: number } | null} id For `set`: the id of the inserted item. For `delete`: the id of the removed item.
 * @property {boolean} ambiguous Whether the written or removed content is a Yjs type or a subdocument.
 * @property {MapWriteSnapshot} snapshot
 */

/**
 * @typedef {Object} MapConflictResolution
 * @property {MapWrite|null} winner The write that determines the value of the key after the transaction (null if rejected or unknown).
 * @property {string} strategy
 * @property {boolean} deterministic
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId Root type name, or `client:clock` of the item that holds a nested type.
 * @property {'set-set'|'delete-set'} type
 * @property {boolean} ambiguous True if any of the writes involves a Yjs type or a subdocument.
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
 * @typedef {YType|string|ID} MapParentRef
 */

/**
 * @typedef {Object} MapWriteLog
 * @property {MapParentRef} parent
 * @property {string} parentId
 * @property {string} key
 * @property {Array<MapWrite>} writes
 */

/**
 * @typedef {Object} PendingMapWrite
 * @property {string} entryKey
 * @property {MapParentRef} parent
 * @property {MapWrite} write
 */

const mapConflictPolicies = ['allow', 'collect', 'error']

/**
 * @param {any} policy
 * @return {MapConflictPolicy}
 */
export const validateMapConflictPolicy = policy => {
  if (!mapConflictPolicies.includes(policy)) {
    throw new Error(`Invalid mapConflictPolicy ${JSON.stringify(policy)}. Expected one of "allow", "collect", "error".`)
  }
  return policy
}

export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(conflicts.length === 1 ? conflicts[0].message : `${conflicts.length} map conflicts: ${conflicts.map(c => c.message).join(' | ')}`)
    this.name = 'MapConflictError'
    this.conflicts = conflicts
  }
}

/**
 * @param {number} client
 * @param {number} clock
 */
const idToString = (client, clock) => client + ':' + clock

/**
 * @param {boolean} isRoot
 * @param {string} parentId
 * @param {string} key
 */
const createEntryKey = (isRoot, parentId, key) => (isRoot ? 'r' : 'n') + parentId + '\u0000' + key

/**
 * @param {YType} type
 */
const getTypeParentId = type => type._item === null ? findRootTypeKey(type) : idToString(type._item.id.client, type._item.id.clock)

/**
 * @param {any} value
 * @return {string}
 */
const stringifyValue = value => {
  /**
   * @type {string|undefined}
   */
  let s
  try {
    s = JSON.stringify(value, (_k, v) => typeof v === 'bigint' ? v.toString() + 'n' : v)
  } catch (_e) {}
  if (s === undefined) {
    s = String(value)
  }
  return s.length > 64 ? s.slice(0, 63) + '…' : s
}

/**
 * @param {any} value
 * @return {{ kind: MapWriteSnapshot['kind'], text: string }}
 */
const describeValue = value => {
  if (value instanceof YType) {
    return { kind: 'type', text: value.name == null ? '<Y.Type>' : `<Y.Type ${value.name}>` }
  }
  if (value instanceof Doc) {
    return { kind: 'doc', text: `<Y.Doc ${value.guid}>` }
  }
  if (value instanceof Uint8Array) {
    return { kind: 'binary', text: `<Uint8Array(${value.length})>` }
  }
  return { kind: 'any', text: stringifyValue(value) }
}

/**
 * @param {import('../internals.js').AbstractContent} content
 * @return {{ kind: MapWriteSnapshot['kind'], text: string }}
 */
const describeContent = content => {
  switch (content.constructor) {
    case ContentType:
      return describeValue(/** @type {ContentType} */ (content).type)
    case ContentDoc:
      return describeValue(/** @type {ContentDoc} */ (content).doc)
    case ContentBinary:
      return describeValue(/** @type {ContentBinary} */ (content).content)
    case ContentAny:
    case ContentJSON: {
      const arr = /** @type {ContentAny|ContentJSON} */ (content).arr
      return { kind: 'any', text: stringifyValue(arr[arr.length - 1]) }
    }
    case ContentDeleted:
      return { kind: 'deleted', text: '<deleted>' }
    default:
      return { kind: 'other', text: `<${content.constructor.name}>` }
  }
}

/**
 * @param {'set'|'delete'} op
 * @param {string} key
 * @param {string} parentId
 * @param {'local'|'remote'} source
 * @param {ID|null} id
 * @param {{ kind: MapWriteSnapshot['kind'], text: string }} desc
 * @return {MapWrite}
 */
const createWrite = (op, key, parentId, source, id, desc) => ({
  op,
  key,
  parentId,
  source,
  id: id === null ? null : { client: id.client, clock: id.clock },
  ambiguous: desc.kind === 'type' || desc.kind === 'doc',
  snapshot: {
    kind: desc.kind,
    summary: op === 'set' ? `set ${JSON.stringify(key)} = ${desc.text}` : `delete ${JSON.stringify(key)} (was ${desc.text})`
  }
})

/**
 * @param {YType} parent
 * @param {string} key
 * @param {any} value
 * @param {ID|null} id
 * @return {PendingMapWrite}
 */
export const createLocalMapSetWrite = (parent, key, value, id) => {
  const parentId = getTypeParentId(parent)
  return { entryKey: createEntryKey(parent._item === null, parentId, key), parent, write: createWrite('set', key, parentId, 'local', id, describeValue(value)) }
}

/**
 * Returns null if there is nothing to delete.
 *
 * @param {YType} parent
 * @param {string} key
 * @return {PendingMapWrite|null}
 */
export const createLocalMapDeleteWrite = (parent, key) => {
  const item = parent._map.get(key)
  if (item === undefined || item.deleted) {
    return null
  }
  const parentId = getTypeParentId(parent)
  return { entryKey: createEntryKey(parent._item === null, parentId, key), parent, write: createWrite('delete', key, parentId, 'local', item.id, describeContent(item.content)) }
}

/**
 * @param {string} parentId
 * @param {string} key
 * @param {Array<MapWrite>} writes
 * @param {function('local'|'remote'|'mixed', boolean):MapConflictResolution} resolve
 * @return {MapConflict|null}
 */
const createConflict = (parentId, key, writes, resolve) => {
  let sets = 0
  let deletes = 0
  writes.forEach(w => { w.op === 'set' ? sets++ : deletes++ })
  if (sets < 2 && (sets === 0 || deletes === 0)) {
    return null
  }
  const type = deletes > 0 ? 'delete-set' : 'set-set'
  const ambiguous = writes.some(w => w.ambiguous)
  const source = writes.every(w => w.source === writes[0].source) ? writes[0].source : 'mixed'
  return {
    key,
    parentId,
    type,
    ambiguous,
    source,
    message: `Map conflict (${type}${ambiguous ? ', ambiguous' : ''}) on key ${JSON.stringify(key)} of parent ${JSON.stringify(parentId)} [${source}]: ${writes.map(w => w.snapshot.summary).join('; ')}`,
    writes: writes.slice(),
    resolution: resolve(source, ambiguous)
  }
}

/**
 * @type {function('local'|'remote'|'mixed', boolean):MapConflictResolution}
 */
const rejectResolution = () => ({ winner: null, strategy: 'reject', deterministic: true })

/**
 * In `error` mode, throws a {@link MapConflictError} if `pending` conflicts with itself or with
 * the writes that already happened in this transaction. Nothing is recorded.
 *
 * @param {Transaction} tr
 * @param {Array<PendingMapWrite>} pending
 */
export const checkMapWrites = (tr, pending) => {
  if (tr.doc.mapConflictPolicy !== 'error' || pending.length === 0) {
    return
  }
  /**
   * @type {Map<string, Array<PendingMapWrite>>}
   */
  const groups = new Map()
  pending.forEach(p => map.setIfUndefined(groups, p.entryKey, () => /** @type {Array<PendingMapWrite>} */ ([])).push(p))
  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  groups.forEach((group, entryKey) => {
    const { key, parentId } = group[0].write
    const existing = tr._mapWrites?.get(entryKey)?.writes ?? []
    const conflict = createConflict(parentId, key, existing.concat(group.map(p => p.write)), rejectResolution)
    if (conflict !== null) {
      conflicts.push(conflict)
    }
  })
  if (conflicts.length > 0) {
    throw new MapConflictError(conflicts)
  }
}

/**
 * Checks `pending` according to the conflict policy and adds the writes to the transaction.
 *
 * @param {Transaction} tr
 * @param {Array<PendingMapWrite>} pending
 */
export const recordMapWrites = (tr, pending) => {
  checkMapWrites(tr, pending)
  if (pending.length === 0) {
    return
  }
  const logs = tr._mapWrites || (tr._mapWrites = new Map())
  pending.forEach(({ entryKey, parent, write }) => {
    map.setIfUndefined(logs, entryKey, () => /** @type {MapWriteLog} */ ({ parent, parentId: write.parentId, key: write.key, writes: [] })).writes.push(write)
  })
}

/**
 * @param {Array<GC|Item>} structs
 * @param {number} clock
 * @return {number} index of the struct that contains clock, or -1
 */
const findStructIndex = (structs, clock) => {
  if (structs.length === 0) {
    return -1
  }
  const last = structs[structs.length - 1]
  if (clock < structs[0].id.clock || clock >= last.id.clock + last.length) {
    return -1
  }
  return findIndexSS(structs, clock)
}

/**
 * @param {Map<number, any>} m
 * @return {Array<number>}
 */
const sortedClients = m => Array.from(m.keys()).sort((a, b) => a - b)

/**
 * Compute the map writes of a remote update before it is integrated.
 *
 * Deletions of items that are replaced by a new value in the same update (i.e. the deleted item is
 * the `origin` of a new value for the same key) are part of that `set` and are not reported as
 * separate `delete` writes.
 *
 * @param {Transaction} tr
 * @param {BlockSet} blocks Structs of the update that are not yet known to the document
 * @param {IdSet} ds Delete set of the update
 * @return {Array<PendingMapWrite>}
 */
export const collectRemoteMapWrites = (tr, blocks, ds) => {
  const store = tr.doc.store
  /**
   * @param {ID} id
   * @return {Item|GC|null}
   */
  const findStruct = id => {
    const refs = blocks.clients.get(id.client)?.refs
    if (refs != null) {
      const i = findStructIndex(refs, id.clock)
      const s = i >= 0 ? refs[i] : null
      if (s instanceof Item || s instanceof GC) {
        return s
      }
    }
    const structs = store.clients.get(id.client)
    if (structs != null && id.clock < getState(store, id.client) && !store.skips.hasId(id)) {
      const s = structs[findIndexSS(structs, id.clock)]
      if (s instanceof Item || s instanceof GC) {
        return s
      }
    }
    return null
  }
  /**
   * @typedef {{ parent: MapParentRef, parentId: string, entryKey: string, key: string }} MapTarget
   */
  /**
   * @type {Map<Item, MapTarget|null>}
   */
  const targets = new Map()
  /**
   * Mirrors how `Item.getMissing` computes parent & parentSub, without mutating the item.
   *
   * @param {Item} item
   * @return {MapTarget|null}
   */
  const resolveTarget = item => {
    /**
     * @type {Array<Item>}
     */
    const chain = []
    /**
     * @type {MapTarget|null}
     */
    let result = null
    let cur = item
    while (true) {
      const known = targets.get(cur)
      if (known !== undefined) {
        result = known
        break
      }
      targets.set(cur, null)
      chain.push(cur)
      const parent = /** @type {YType|ID|string|null} */ (cur.parent)
      const key = cur.parentSub
      if (parent !== null) {
        if (key === null) {
          result = null
        } else if (parent instanceof YType) {
          const parentId = getTypeParentId(parent)
          result = { parent, parentId, entryKey: createEntryKey(parent._item === null, parentId, key), key }
        } else if (typeof parent === 'string') {
          result = { parent, parentId: parent, entryKey: createEntryKey(true, parent, key), key }
        } else if (!(findStruct(parent) instanceof GC)) {
          const parentId = idToString(parent.client, parent.clock)
          result = { parent, parentId, entryKey: createEntryKey(false, parentId, key), key }
        }
        break
      }
      const left = cur.origin ? findStruct(cur.origin) : null
      const right = cur.rightOrigin ? findStruct(cur.rightOrigin) : null
      if (left instanceof GC || right instanceof GC) {
        break
      }
      const next = left instanceof Item ? left : right
      if (next === null) {
        break
      }
      cur = next
    }
    chain.forEach(c => targets.set(c, result))
    return result
  }
  /**
   * @type {Array<PendingMapWrite>}
   */
  const pending = []
  const replacedIds = new Set()
  sortedClients(blocks.clients).forEach(client => {
    /** @type {Array<Item|GC>} */ (/** @type {any} */ (blocks.clients.get(client)).refs).forEach(struct => {
      if (!(struct instanceof Item)) return
      const target = resolveTarget(struct)
      if (target === null) return
      if (struct.origin !== null) {
        replacedIds.add(idToString(struct.origin.client, struct.origin.clock))
      }
      pending.push({ entryKey: target.entryKey, parent: target.parent, write: createWrite('set', target.key, target.parentId, 'remote', struct.id, describeContent(struct.content)) })
    })
  })
  /**
   * @param {Item|GC} struct
   */
  const addDelete = struct => {
    if (!(struct instanceof Item) || struct.deleted) return
    const lastId = struct.lastId
    if (replacedIds.has(idToString(lastId.client, lastId.clock))) return
    const target = resolveTarget(struct)
    if (target === null) return
    pending.push({ entryKey: target.entryKey, parent: target.parent, write: createWrite('delete', target.key, target.parentId, 'remote', struct.id, describeContent(struct.content)) })
  }
  sortedClients(ds.clients).forEach(client => {
    const structLists = [store.clients.get(client), blocks.clients.get(client)?.refs]
    const ranges = /** @type {import('../internals.js').IdRanges} */ (ds.clients.get(client)).getIds()
    ranges.forEach(({ clock, len }) => {
      structLists.forEach(structs => {
        if (structs == null) return
        let i = findStructIndex(structs, clock)
        if (i < 0 && structs.length > 0 && clock < structs[0].id.clock) {
          i = 0
        }
        for (; i >= 0 && i < structs.length && structs[i].id.clock < clock + len; i++) {
          addDelete(structs[i])
        }
      })
    })
  })
  return pending
}

/**
 * @param {Doc} doc
 * @param {MapParentRef} ref
 * @return {YType|null}
 */
const resolveParentType = (doc, ref) => {
  if (ref instanceof YType) {
    return ref
  }
  if (typeof ref === 'string') {
    return doc.share.get(ref) ?? null
  }
  const structs = doc.store.clients.get(ref.client)
  if (structs == null || ref.clock >= getState(doc.store, ref.client)) {
    return null
  }
  const item = structs[findIndexSS(structs, ref.clock)]
  return item instanceof Item && item.content instanceof ContentType ? item.content.type : null
}

/**
 * Called when a transaction ends. Records the conflicts of the transaction in `doc._mapConflicts`
 * if the policy is `collect`.
 *
 * @param {Transaction} tr
 */
export const collectMapConflicts = tr => {
  const doc = tr.doc
  const logs = tr._mapWrites
  tr._mapWrites = null
  if (logs === null || doc.mapConflictPolicy !== 'collect') {
    return
  }
  logs.forEach(log => {
    const conflict = createConflict(log.parentId, log.key, log.writes, (source, ambiguous) => {
      const current = resolveParentType(doc, log.parent)?._map.get(log.key)
      /**
       * @type {MapWrite|null}
       */
      let winner = null
      if (current !== undefined) {
        for (let i = log.writes.length - 1; i >= 0 && winner === null; i--) {
          const w = log.writes[i]
          if (current.deleted
            ? w.op === 'delete'
            : (w.op === 'set' && w.id !== null && w.id.client === current.id.client && w.id.clock >= current.id.clock && w.id.clock < current.id.clock + current.length)) {
            winner = w
          }
        }
      }
      return {
        winner,
        strategy: source === 'local' ? 'last-write-wins' : 'crdt-order',
        // the outcome of conflicts on nested types / subdocs depends on content that is not part of the conflict
        deterministic: !ambiguous
      }
    })
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
  Object.defineProperty(obj, key, { value: (Object.prototype.hasOwnProperty.call(obj, key) ? obj[key] : 0) + 1, enumerable: true, writable: true, configurable: true })
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
