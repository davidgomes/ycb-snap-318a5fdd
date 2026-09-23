import {
  Item, ContentType, ContentDoc, ID, getState, getItem, findIndexSS, decodeUpdateV2, UpdateDecoderV1, UpdateDecoderV2, // eslint-disable-line
  Transaction, Doc // eslint-disable-line
} from '../internals.js'

import { YType } from '../ytype.js' // eslint-disable-line

/**
 * @typedef {'allow'|'collect'|'error'} MapConflictPolicy
 */

/**
 * @typedef {Object} MapWrite
 * @property {'set'|'delete'} op
 * @property {'local'|'remote'} source
 * @property {string|null} id
 * @property {boolean} ambiguous
 * @property {{ summary: string, value?: any }} snapshot
 */

/**
 * @typedef {Object} MapConflict
 * @property {string} key
 * @property {string} parentId
 * @property {'set-set'|'delete-set'|'ambiguous'} type
 * @property {'set-set'|'delete-set'} kind
 * @property {boolean} ambiguous
 * @property {'local'|'remote'|'mixed'} source
 * @property {string} message
 * @property {Array<MapWrite>} writes
 * @property {{ winner: MapWrite|null, strategy: string, deterministic: boolean }} resolution
 */

export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(`Map conflict detected: ${conflicts.map(c => c.message).join('; ')}`)
    this.name = 'MapConflictError'
    this.conflicts = conflicts
  }
}

/**
 * @param {ID} id
 */
const idToString = id => `${id.client}:${id.clock}`

/**
 * @param {Doc} doc
 * @param {YType} type
 * @return {string}
 */
const typeToParentId = (doc, type) => {
  if (type._item !== null) return idToString(type._item.id)
  for (const [name, t] of doc.share) {
    if (t === type) return name
  }
  return '<unknown>'
}

/**
 * @param {'set'|'delete'} op
 * @param {string} key
 * @param {any} value
 * @param {boolean} isType
 * @param {boolean} isDoc
 */
const summarize = (op, key, value, isType, isDoc) => {
  if (op === 'delete') return `delete ${JSON.stringify(key)}`
  let v
  if (isType) v = '<Y.Type>'
  else if (isDoc) v = '<Y.Doc>'
  else {
    try {
      v = JSON.stringify(value)
    } catch (_e) {
      v = String(value)
    }
    if (v === undefined) v = String(value)
    if (v.length > 80) v = v.slice(0, 77) + '...'
  }
  return `set ${JSON.stringify(key)} = ${v}`
}

/**
 * @param {Item} item
 */
const itemIsAmbiguous = item => item.content instanceof ContentType || item.content instanceof ContentDoc

/**
 * @param {Item} item
 */
const itemValue = item => {
  if (itemIsAmbiguous(item)) return undefined
  const c = item.content.getContent()
  return c[c.length - 1]
}

/**
 * @param {string} parentId
 * @param {string} key
 * @param {Array<MapWrite>} writes
 * @param {{ winner: MapWrite|null, strategy: string }} res
 * @return {MapConflict}
 */
const createConflict = (parentId, key, writes, res) => {
  const kind = writes.some(w => w.op === 'delete') ? 'delete-set' : 'set-set'
  const ambiguous = writes.some(w => w.ambiguous)
  const sources = new Set(writes.map(w => w.source))
  const source = sources.size > 1 ? 'mixed' : /** @type {'local'|'remote'} */ (writes[0].source)
  return {
    key,
    parentId,
    type: ambiguous ? 'ambiguous' : kind,
    kind,
    ambiguous,
    source,
    message: `${ambiguous ? 'ambiguous ' : ''}${kind} conflict on key ${JSON.stringify(key)} of parent ${parentId} (${source}): ${writes.map(w => w.snapshot.summary).join(', ')}`,
    writes,
    resolution: { winner: res.winner, strategy: res.strategy, deterministic: true }
  }
}

/**
 * @param {Doc} doc
 * @param {Array<MapConflict>} conflicts
 */
const reportConflicts = (doc, conflicts) => {
  if (conflicts.length === 0) return
  if (doc.mapConflictPolicy === 'error') throw new MapConflictError(conflicts)
  doc._mapConflicts.push(...conflicts)
}

/**
 * @type {WeakMap<Transaction, Map<string, { parentId: string, key: string, writes: Array<MapWrite> }>>}
 */
const localWrites = new WeakMap()

/**
 * Must be called before a local map write is applied.
 *
 * @param {Transaction} transaction
 * @param {YType} parent
 * @param {string} key
 * @param {'set'|'delete'} op
 * @param {any} [value]
 */
export const checkLocalMapWrite = (transaction, parent, key, op, value) => {
  const doc = transaction.doc
  if (doc.mapConflictPolicy === 'allow') return
  const parentId = typeToParentId(doc, parent)
  const prev = parent._map.get(key)
  if (op === 'delete' && (prev === undefined || prev.deleted)) return
  let writesByKey = localWrites.get(transaction)
  if (writesByKey === undefined) {
    writesByKey = new Map()
    localWrites.set(transaction, writesByKey)
  }
  const mapKey = parentId + '\u0000' + key
  const isType = value instanceof YType
  const isDoc = value instanceof Doc
  /**
   * @type {MapWrite}
   */
  const write = {
    op,
    source: 'local',
    id: op === 'set' ? `${doc.clientID}:${getState(doc.store, doc.clientID)}` : null,
    ambiguous: isType || isDoc || (op === 'delete' && prev !== undefined && itemIsAmbiguous(prev)),
    snapshot: op === 'set' && !isType && !isDoc ? { summary: summarize(op, key, value, isType, isDoc), value } : { summary: summarize(op, key, value, isType, isDoc) }
  }
  const entry = writesByKey.get(mapKey)
  if (entry === undefined) {
    writesByKey.set(mapKey, { parentId, key, writes: [write] })
    return
  }
  const writes = [...entry.writes, write]
  reportConflicts(doc, [createConflict(parentId, key, writes, {
    winner: write.op === 'set' ? write : null,
    strategy: 'last-write-in-transaction'
  })])
  entry.writes = writes
}

/**
 * Analyze an update before it is applied. Throws in `error` mode, so the update is never partially
 * applied.
 *
 * @param {Doc} doc
 * @param {Uint8Array} update
 * @param {typeof UpdateDecoderV1 | typeof UpdateDecoderV2} YDecoder
 */
export const checkUpdateMapConflicts = (doc, update, YDecoder) => {
  if (doc.mapConflictPolicy === 'allow') return
  const store = doc.store
  const { structs, ds } = decodeUpdateV2(update, YDecoder)
  /**
   * @type {Map<string, Item>}
   */
  const newItems = new Map()
  structs.forEach(s => {
    if (s instanceof Item && s.id.clock >= getState(store, s.id.client)) newItems.set(idToString(s.id), s)
  })
  /**
   * @type {Map<Item, { parentId: string, key: string|null }|null>}
   */
  const resolved = new Map()
  /**
   * @param {Item} item
   * @return {{ parentId: string, key: string|null }|null}
   */
  const resolve = item => {
    if (resolved.has(item)) return /** @type {any} */ (resolved.get(item))
    resolved.set(item, null)
    let res = null
    const parent = /** @type {any} */ (item.parent)
    if (parent instanceof YType) {
      res = { parentId: typeToParentId(doc, parent), key: item.parentSub }
    } else if (typeof parent === 'string') {
      res = { parentId: parent, key: item.parentSub }
    } else if (parent instanceof ID) {
      res = { parentId: idToString(parent), key: item.parentSub }
    } else {
      const ref = item.origin || item.rightOrigin
      if (ref !== null) {
        const n = newItems.get(idToString(ref))
        if (n !== undefined) {
          res = resolve(n)
        } else if (ref.clock < getState(store, ref.client)) {
          const s = getItem(store, ref)
          if (s instanceof Item) res = resolve(s)
        }
      }
    }
    resolved.set(item, res)
    return res
  }
  /**
   * @type {Map<string, { parentId: string, key: string, sets: Array<Item>, deletes: Array<Item> }>}
   */
  const groups = new Map()
  /**
   * @param {Item} item
   * @param {'sets'|'deletes'} kind
   */
  const add = (item, kind) => {
    const r = resolve(item)
    if (r === null || r.key === null) return
    const k = r.parentId + '\u0000' + r.key
    let g = groups.get(k)
    if (g === undefined) {
      g = { parentId: r.parentId, key: r.key, sets: [], deletes: [] }
      groups.set(k, g)
    }
    if (!g[kind].includes(item)) g[kind].push(item)
  }
  newItems.forEach(item => add(item, 'sets'))
  ds.forEach((range, client) => {
    const end = range.clock + range.len
    newItems.forEach(item => {
      if (item.id.client === client && item.id.clock >= range.clock && item.id.clock < end) add(item, 'deletes')
    })
    const structsOfClient = store.clients.get(client)
    if (structsOfClient === undefined || range.clock >= getState(store, client)) return
    for (let i = findIndexSS(structsOfClient, range.clock); i < structsOfClient.length && structsOfClient[i].id.clock < end; i++) {
      const s = structsOfClient[i]
      if (s instanceof Item && !s.deleted) add(s, 'deletes')
    }
  })
  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  const sortedKeys = Array.from(groups.keys()).sort()
  for (const k of sortedKeys) {
    const g = /** @type {any} */ (groups.get(k))
    const allSetsDeleted = g.sets.every(/** @param {Item} s */ s => ds.hasId(s.id))
    // deleting the value that a new set overwrites is a regular overwrite, not a conflict
    const deletes = g.deletes.filter(/** @param {Item} d */ d => allSetsDeleted || !g.sets.some(/** @param {Item} s */ s => s.origin !== null && s.origin.client === d.id.client && s.origin.clock >= d.id.clock && s.origin.clock < d.id.clock + d.length))
    if (g.sets.length === 0 || (g.sets.length < 2 && deletes.length === 0)) continue
    const sets = g.sets.slice().sort(/** @param {Item} a @param {Item} b */ (a, b) => a.id.client - b.id.client || a.id.clock - b.id.clock)
    /**
     * @type {Array<MapWrite>}
     */
    const writes = sets.map(/** @param {Item} s */ s => {
      const ambiguous = itemIsAmbiguous(s)
      const value = itemValue(s)
      return {
        op: 'set',
        source: 'remote',
        id: idToString(s.id),
        ambiguous,
        snapshot: ambiguous ? { summary: summarize('set', g.key, value, s.content instanceof ContentType, s.content instanceof ContentDoc) } : { summary: summarize('set', g.key, value, false, false), value }
      }
    })
    deletes.slice().sort(/** @param {Item} a @param {Item} b */ (a, b) => a.id.client - b.id.client || a.id.clock - b.id.clock).forEach(/** @param {Item} d */ d => {
      writes.push({
        op: 'delete',
        source: 'remote',
        id: idToString(d.id),
        ambiguous: itemIsAmbiguous(d),
        snapshot: { summary: summarize('delete', g.key, undefined, false, false) }
      })
    })
    const liveSets = sets.filter(/** @param {Item} s */ s => !ds.hasId(s.id))
    const overwritten = new Set(liveSets.filter(/** @param {Item} s */ s => s.origin !== null).map(/** @param {Item} s */ s => idToString(/** @type {ID} */ (s.origin))))
    const heads = liveSets.filter(/** @param {Item} s */ s => !overwritten.has(idToString(s.id)))
    const winnerItem = heads.length > 0 ? heads[heads.length - 1] : null
    const winner = winnerItem === null ? null : /** @type {MapWrite} */ (writes.find(w => w.id === idToString(winnerItem.id)))
    conflicts.push(createConflict(g.parentId, g.key, writes, {
      winner,
      strategy: winner === null ? 'delete-wins' : 'crdt-order-highest-client'
    }))
  }
  reportConflicts(doc, conflicts)
}

/**
 * @param {Array<MapConflict>} conflicts
 */
export const summarizeMapConflicts = conflicts => {
  /**
   * @param {function(MapConflict):string} f
   */
  const countBy = f => {
    /**
     * @type {Object<string,number>}
     */
    const res = {}
    conflicts.forEach(c => {
      const k = f(c)
      res[k] = (res[k] || 0) + 1
    })
    return res
  }
  return {
    count: conflicts.length,
    total: conflicts.length,
    byType: countBy(c => c.type),
    byKey: countBy(c => c.key),
    byParent: countBy(c => c.parentId),
    bySource: countBy(c => c.source)
  }
}
