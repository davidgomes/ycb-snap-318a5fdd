/**
 * Strict conflict detection for map-style (attribute) writes.
 *
 * A conflict is reported when the same key of the same parent type receives several sets, or a set
 * and a delete, within a single transaction or a single (merged) update. Detection is enabled via
 * `new Y.Doc({ mapConflictPolicy: 'collect' | 'error' })`.
 *
 * @module MapConflicts
 */

import {
  getState,
  getItem,
  findIndexSS,
  findRootTypeKey,
  readBlockSet,
  readIdSet,
  mergeIdSets,
  createID,
  UpdateDecoderV2,
  ContentAny,
  ContentBinary,
  ContentDeleted,
  ContentDoc,
  ContentJSON,
  ContentType,
  GC,
  ID,
  Item,
  Skip,
  AbstractContent, BlockSet, Doc, IdSet, StructStore, Transaction, YType // eslint-disable-line
} from '../internals.js'

import * as array from 'lib0/array'
import * as decoding from 'lib0/decoding'
import * as error from 'lib0/error'
import * as map from 'lib0/map'
import * as math from 'lib0/math'
import * as object from 'lib0/object'

/**
 * - `allow`: map writes are applied without conflict detection (default).
 * - `collect`: conflicts are recorded and can be retrieved via `ydoc.getMapConflicts()`.
 * - `error`: conflicting writes throw a `MapConflictError` before they are applied.
 *
 * @typedef {'allow'|'collect'|'error'} MapConflictPolicy
 */

/**
 * @typedef {Object} MapWriteSnapshot
 * @property {string} summary Human readable description of the write, e.g. `set "title" = "Hi"`
 * @property {'any'|'json'|'binary'|'type'|'doc'|'deleted'|'unknown'} valueType
 * @property {any} [value] The written (or removed) value. Only defined for plain values and binaries.
 * @property {string|null} [typeName] Name of the written (or removed) Yjs type
 * @property {string} [guid] Guid of the written (or removed) subdocument
 */

/**
 * @typedef {Object} MapWrite
 * @property {'set'|'delete'} op
 * @property {string} key
 * @property {string} parentId
 * @property {string} id `client:clock` of the inserted item (set) or of the removed item (delete)
 * @property {number} client
 * @property {number} clock
 * @property {'local'|'remote'|'existing'} source `existing` is only used by `resolution.winner` if
 *   a value that was written before the conflicting writes is retained.
 * @property {boolean} ambiguous Whether the written or removed value is a Yjs type or a subdocument
 * @property {MapWriteSnapshot} snapshot
 */

/**
 * @typedef {Object} MapConflictResolution
 * @property {MapWrite|null} winner The write that determines the value of the key
 * @property {'last-write-wins'|'client-id-order'|'unresolved'} strategy `last-write-wins` if the
 *   winner causally follows all other sets, `client-id-order` if concurrent sets were ordered by
 *   client id.
 * @property {boolean} deterministic Whether all peers resolve the conflict to the same winner
 */

/**
 * @typedef {Object} MapConflict
 * @property {'set-set'|'delete-set'|'ambiguous'} type `ambiguous` if a Yjs type or a subdocument is
 *   involved, otherwise equal to `kind`.
 * @property {'set-set'|'delete-set'} kind
 * @property {boolean} ambiguous
 * @property {string} key
 * @property {string} parentId `root:<name>` for top-level types, otherwise the `client:clock` id of
 *   the item that contains the parent type.
 * @property {'local'|'remote'|'mixed'} source
 * @property {string} message
 * @property {Array<MapWrite>} writes
 * @property {MapConflictResolution} resolution
 */

/**
 * @typedef {Object} MapConflictSummary
 * @property {number} count
 * @property {number} total
 * @property {{[type:string]:number}} byType
 * @property {{[key:string]:number}} byKey
 * @property {{[parentId:string]:number}} byParent
 * @property {{[source:string]:number}} bySource
 */

/**
 * @typedef {{ parent: YType|ID|string, parentSub: string|null }} ResolvedParent
 */

/**
 * @typedef {{ client: number, clock: number }} KeyNode
 */

export class MapConflictError extends Error {
  /**
   * @param {Array<MapConflict>} conflicts
   */
  constructor (conflicts) {
    super(`Rejected by mapConflictPolicy "error": ${conflicts[0].message}` + (conflicts.length > 1 ? ` (and ${conflicts.length - 1} more conflicts)` : ''))
    this.name = 'MapConflictError'
    /**
     * @type {Array<MapConflict>}
     */
    this.conflicts = conflicts
  }
}

/**
 * @param {any} policy
 * @return {MapConflictPolicy}
 */
export const validateMapConflictPolicy = policy => {
  if (policy === 'allow' || policy === 'collect' || policy === 'error') {
    return policy
  }
  throw error.create(`Invalid mapConflictPolicy ${JSON.stringify(policy)}. Expected "allow", "collect", or "error".`)
}

/**
 * @param {number} client
 * @param {number} clock
 */
const idToString = (client, clock) => client + ':' + clock

/**
 * @param {YType|ID|string} parent
 * @return {string}
 */
export const getMapParentId = parent => {
  if (typeof parent === 'string') {
    return 'root:' + parent
  }
  if (parent instanceof ID) {
    return idToString(parent.client, parent.clock)
  }
  const item = parent._item
  return item === null ? 'root:' + findRootTypeKey(parent) : idToString(item.id.client, item.id.clock)
}

/**
 * @param {Doc} doc
 * @param {YType|ID|string} parent
 * @return {YType|null}
 */
const getParentType = (doc, parent) => {
  if (typeof parent === 'string') {
    return doc.share.get(parent) ?? null
  }
  if (parent instanceof ID) {
    if (parent.clock >= getState(doc.store, parent.client) || doc.store.skips.hasId(parent)) {
      return null
    }
    const item = /** @type {Item|GC} */ (getItem(doc.store, parent))
    return item instanceof Item && item.content instanceof ContentType ? item.content.type : null
  }
  return parent
}

const maxPreviewLength = 60

/**
 * @param {any} value
 * @return {string}
 */
const previewValue = value => {
  /**
   * @type {string|undefined}
   */
  let str
  if (typeof value !== 'bigint') {
    try {
      str = JSON.stringify(value, (_key, v) => typeof v === 'bigint' ? v.toString() + 'n' : v)
    } catch (_e) {}
  }
  if (str === undefined) {
    str = typeof value === 'bigint' ? value.toString() + 'n' : String(value)
  }
  return str.length > maxPreviewLength ? str.slice(0, maxPreviewLength - 3) + '...' : str
}

/**
 * @param {AbstractContent} content
 * @param {number} offset
 * @return {{ text: string, ambiguous: boolean, snapshot: Omit<MapWriteSnapshot,'summary'> }}
 */
const describeContent = (content, offset) => {
  switch (content.constructor) {
    case ContentAny:
    case ContentJSON: {
      const value = /** @type {ContentAny} */ (content).arr[offset]
      return { text: previewValue(value), ambiguous: false, snapshot: { valueType: content.constructor === ContentAny ? 'any' : 'json', value } }
    }
    case ContentBinary: {
      const value = /** @type {ContentBinary} */ (content).content
      return { text: `Uint8Array(${value.length})`, ambiguous: false, snapshot: { valueType: 'binary', value } }
    }
    case ContentType: {
      const typeName = /** @type {ContentType} */ (content).type.name ?? null
      return { text: typeName === null ? 'Y.Type' : `Y.Type<${typeName}>`, ambiguous: true, snapshot: { valueType: 'type', typeName } }
    }
    case ContentDoc: {
      const guid = /** @type {ContentDoc} */ (content).doc.guid
      return { text: `Y.Doc(${guid})`, ambiguous: true, snapshot: { valueType: 'doc', guid } }
    }
    case ContentDeleted:
      return { text: '<garbage collected>', ambiguous: false, snapshot: { valueType: 'deleted' } }
    default:
      return { text: `<${content.constructor.name}>`, ambiguous: false, snapshot: { valueType: 'unknown' } }
  }
}

/**
 * @param {'set'|'delete'} op
 * @param {string} parentId
 * @param {string} key
 * @param {number} client
 * @param {number} clock
 * @param {'local'|'remote'|'existing'} source
 * @param {AbstractContent} content Content of the inserted item (set) or of the removed item (delete)
 * @param {number} offset Position of the value in `content`
 * @return {MapWrite}
 */
const createMapWrite = (op, parentId, key, client, clock, source, content, offset) => {
  const { text, ambiguous, snapshot } = describeContent(content, offset)
  const summary = op === 'set' ? `set ${JSON.stringify(key)} = ${text}` : `delete ${JSON.stringify(key)} (was ${text})`
  return { op, key, parentId, id: idToString(client, clock), client, clock, source, ambiguous, snapshot: { summary, ...snapshot } }
}

/**
 * @param {Item} item
 * @param {number} clock
 * @param {string} parentId
 * @param {string} key
 * @param {'local'|'remote'|'existing'} source
 * @return {MapWrite}
 */
const describeItemValue = (item, clock, parentId, key, source) =>
  createMapWrite(item.deleted ? 'delete' : 'set', parentId, key, item.id.client, clock, source, item.content, clock - item.id.clock)

/**
 * @param {Array<MapWrite>} writes
 * @return {'set-set'|'delete-set'|null}
 */
const classifyMapWrites = writes => {
  let sets = 0
  let deletes = 0
  writes.forEach(write => {
    if (write.op === 'set') {
      sets++
    } else {
      deletes++
    }
  })
  return sets > 1 ? 'set-set' : (sets === 1 && deletes > 0 ? 'delete-set' : null)
}

/**
 * The items of a map key form a chain that is always integrated with `rightOrigin = null`. YATA
 * therefore orders the chain like a pre-order traversal of the origin tree in which siblings are
 * sorted by ascending client id. The item that holds the value of the key (the rightmost item) is
 * found by repeatedly descending into the child with the highest client id.
 */
class KeyTree {
  constructor () {
    /**
     * @type {Map<string,{ client: number, clock: number, origin: string|null }>}
     */
    this.nodes = new Map()
  }

  /**
   * @param {number} client
   * @param {number} clock
   * @param {ID|null} origin
   */
  add (client, clock, origin) {
    this.nodes.set(idToString(client, clock), { client, clock, origin: origin && idToString(origin.client, origin.clock) })
  }

  /**
   * Add the integrated chain that ends with `item`.
   *
   * @param {Item|null|undefined} item
   */
  addChain (item) {
    for (; item != null; item = item.left) {
      const { client, clock } = item.id
      for (let i = 0; i < item.length; i++) {
        this.add(client, clock + i, i === 0 ? item.origin : createID(client, clock + i - 1))
      }
    }
  }

  /**
   * @return {KeyNode|null}
   */
  rightmost () {
    /**
     * @type {Map<string|null,Array<KeyNode>>}
     */
    const children = new Map()
    this.nodes.forEach(node => {
      const parent = node.origin !== null && this.nodes.has(node.origin) ? node.origin : null
      map.setIfUndefined(children, parent, () => /** @type {Array<KeyNode>} */ ([])).push(node)
    })
    /**
     * @type {KeyNode|null}
     */
    let result = null
    let kids = children.get(null)
    while (kids !== undefined) {
      const next = kids.reduce((best, node) => (node.client > best.client || (node.client === best.client && node.clock < best.clock)) ? node : best)
      result = next
      kids = children.get(idToString(next.client, next.clock))
    }
    return result
  }

  /**
   * @param {string} id
   * @return {Set<string>} `id` and all of its (transitive) origins
   */
  ancestors (id) {
    const res = new Set()
    for (let current = /** @type {string|null} */ (id); current !== null && !res.has(current); current = this.nodes.get(current)?.origin ?? null) {
      res.add(current)
    }
    return res
  }
}

/**
 * @param {Array<MapWrite>} writes
 * @param {KeyTree} tree
 * @param {KeyNode|null} rightmost The item that holds the value of the key after all writes are applied
 * @param {boolean} rightmostDeleted
 * @param {function(number,number):MapWrite|null} describeRightmost Describes `rightmost` if it is not
 *   one of `writes`
 * @return {MapConflictResolution}
 */
const resolveMapConflict = (writes, tree, rightmost, rightmostDeleted, describeRightmost) => {
  if (rightmost === null) {
    return { winner: null, strategy: 'unresolved', deterministic: false }
  }
  const id = idToString(rightmost.client, rightmost.clock)
  const winner = (rightmostDeleted ? writes.find(write => write.op === 'delete' && write.id === id) : undefined) ??
    writes.find(write => write.op === 'set' && write.id === id) ??
    describeRightmost(rightmost.client, rightmost.clock)
  const ancestors = tree.ancestors(id)
  return {
    winner,
    strategy: writes.some(write => write.op === 'set' && !ancestors.has(write.id)) ? 'client-id-order' : 'last-write-wins',
    deterministic: winner !== null
  }
}

/**
 * @param {string} parentId
 * @param {string} key
 * @param {'set-set'|'delete-set'} kind
 * @param {Array<MapWrite>} writes
 * @param {MapConflictResolution} resolution
 * @return {MapConflict}
 */
const createMapConflict = (parentId, key, kind, writes, resolution) => {
  const { winner, strategy } = resolution
  const ambiguous = writes.some(write => write.ambiguous) || (winner !== null && winner.ambiguous)
  const hasLocal = writes.some(write => write.source === 'local')
  const hasRemote = writes.some(write => write.source !== 'local')
  const source = hasLocal && hasRemote ? 'mixed' : (hasLocal ? 'local' : 'remote')
  /**
   * @param {MapWrite} write
   */
  const describe = write => `${write.snapshot.summary} [${write.source} ${write.id}]`
  const listed = writes.slice(0, 5).map(describe).join('; ') + (writes.length > 5 ? `; and ${writes.length - 5} more` : '')
  let message = `Map conflict (${kind}) on key ${JSON.stringify(key)} of ${parentId}: ${writes.length} writes within a single ${source === 'remote' ? 'update' : 'transaction'} (${listed}). `
  message += winner === null ? 'The winning write could not be determined.' : `Resolved by ${strategy}: ${describe(winner)} wins.`
  if (ambiguous) {
    message += ' Ambiguous: a Yjs type or subdocument is involved, its content is replaced instead of merged.'
  }
  return { type: ambiguous ? 'ambiguous' : kind, kind, ambiguous, key, parentId, source, message, writes, resolution }
}

class MapWriteGroup {
  /**
   * @param {YType|ID|string} parent
   * @param {string} parentId
   * @param {string} key
   */
  constructor (parent, parentId, key) {
    this.parent = parent
    this.parentId = parentId
    this.key = key
    /**
     * @type {Array<MapWrite>}
     */
    this.writes = []
  }
}

/**
 * The map writes of a transaction, grouped by parent and key.
 */
export class MapWriteLog {
  constructor () {
    /**
     * @type {Map<string,Map<string,MapWriteGroup>>}
     */
    this.groups = new Map()
    /**
     * @type {Set<string>}
     */
    this.seen = new Set()
  }

  /**
   * @param {string} parentId
   * @param {string} key
   * @return {Array<MapWrite>}
   */
  get (parentId, key) {
    return this.groups.get(parentId)?.get(key)?.writes ?? []
  }

  /**
   * @param {MapWrite} write
   */
  has (write) {
    return this.seen.has(write.op + write.id)
  }

  /**
   * @param {YType|ID|string} parent
   * @param {MapWrite} write
   */
  add (parent, write) {
    const keys = map.setIfUndefined(this.groups, write.parentId, () => /** @type {Map<string,MapWriteGroup>} */ (new Map()))
    map.setIfUndefined(keys, write.key, () => new MapWriteGroup(parent, write.parentId, write.key)).writes.push(write)
    this.seen.add(write.op + write.id)
  }
}

/**
 * @param {Transaction} transaction
 * @return {MapWriteLog}
 */
const getMapWriteLog = transaction => transaction._mapWrites ?? (transaction._mapWrites = new MapWriteLog())

/**
 * Registers a local write to a map key. With `mapConflictPolicy: 'error'`, a conflicting write is
 * rejected before it is applied.
 *
 * @param {Transaction} transaction
 * @param {YType} parent
 * @param {string} key
 * @param {'set'|'delete'} op
 * @param {ID} id Id of the inserted item (set) or of the removed item (delete)
 * @param {AbstractContent} content Content of the inserted item (set) or of the removed item (delete)
 * @param {number} offset Position of the value in `content`
 */
export const trackLocalMapWrite = (transaction, parent, key, op, id, content, offset) => {
  const policy = transaction.doc.mapConflictPolicy
  if (policy === 'allow') {
    return
  }
  const log = getMapWriteLog(transaction)
  const parentId = getMapParentId(parent)
  const write = createMapWrite(op, parentId, key, id.client, id.clock, 'local', content, offset)
  if (policy === 'error') {
    const writes = log.get(parentId, key).concat(write)
    const kind = classifyMapWrites(writes)
    if (kind !== null) {
      // a local write is based on the current value of the key, so it would win
      throw new MapConflictError([createMapConflict(parentId, key, kind, writes, { winner: write, strategy: 'last-write-wins', deterministic: true })])
    }
  }
  log.add(parent, write)
}

/**
 * @param {Array<Item|GC>} structs sorted by clock
 * @param {number} clock
 * @return {number} Index of the first struct that ends after `clock`
 */
const lowerBound = (structs, clock) => {
  let left = 0
  let right = structs.length
  while (left < right) {
    const mid = math.floor((left + right) / 2)
    const struct = structs[mid]
    if (struct.id.clock + struct.length <= clock) {
      left = mid + 1
    } else {
      right = mid
    }
  }
  return left
}

/**
 * The structs of an update (and of the pending structs it unblocks) that are not yet part of the
 * document. Mirrors how `readUpdateV2`, `integrateStructs`, and `Item.getMissing` integrate them.
 */
class UpdateStructs {
  /**
   * @param {StructStore} store
   */
  constructor (store) {
    this.store = store
    /**
     * All added structs, sorted by clock.
     *
     * @type {Map<number,Array<Item|GC>>}
     */
    this.clients = new Map()
    /**
     * @type {Set<Item|GC>}
     */
    this.integrable = new Set()
    /**
     * @type {Map<Item,ResolvedParent|null>}
     */
    this.parents = new Map()
  }

  /**
   * @param {number} client
   * @param {number} clock
   */
  docHas (client, clock) {
    return clock < getState(this.store, client) && !this.store.skips.has(client, clock)
  }

  /**
   * @param {BlockSet} blocks
   * @return {Map<number,Array<Item|GC>>} The structs that were added, sorted by clock
   */
  add (blocks) {
    /**
     * @type {Map<number,Array<Item|GC>>}
     */
    const added = new Map()
    blocks.clients.forEach(({ refs }, client) => {
      const fresh = refs.filter(struct =>
        struct.constructor !== Skip &&
        !(this.docHas(client, struct.id.clock) && this.docHas(client, struct.id.clock + struct.length - 1)) &&
        this.indexOf(client, struct.id.clock) < 0
      )
      if (fresh.length > 0) {
        added.set(client, fresh)
        const structs = map.setIfUndefined(this.clients, client, () => /** @type {Array<Item|GC>} */ ([]))
        structs.push(...fresh)
        structs.sort((a, b) => a.id.clock - b.id.clock)
      }
    })
    return added
  }

  /**
   * @param {number} client
   * @param {number} clock
   * @return {number} Index of the struct that contains `clock`, or -1
   */
  indexOf (client, clock) {
    const structs = this.clients.get(client)
    if (structs === undefined) {
      return -1
    }
    const i = lowerBound(structs, clock)
    return i < structs.length && structs[i].id.clock <= clock ? i : -1
  }

  /**
   * @param {ID} id
   * @return {Item|GC|null}
   */
  find (id) {
    const i = this.indexOf(id.client, id.clock)
    return i < 0 ? null : /** @type {Array<Item|GC>} */ (this.clients.get(id.client))[i]
  }

  /**
   * @param {ID} id
   * @return {{ struct: Item|GC, inDoc: boolean }|null} The struct if it is available once the update is integrated
   */
  get (id) {
    if (this.docHas(id.client, id.clock)) {
      return { struct: /** @type {Item|GC} */ (getItem(this.store, id)), inDoc: true }
    }
    const struct = this.find(id)
    return struct !== null && this.integrable.has(struct) ? { struct, inDoc: false } : null
  }

  /**
   * Mirrors a single `integrateStructs` pass. A struct is integrated once all of its dependencies
   * are available (gaps are filled with skips). If a struct of a client can't be integrated, the
   * remaining structs of that client are postponed as well.
   *
   * @param {Map<number,Array<Item|GC>>} candidates Structs of this pass, sorted by clock
   * @return {Set<number>} Clients with postponed structs
   */
  integrate (candidates) {
    const clients = array.from(candidates.keys()).sort((a, b) => a - b)
    /**
     * @type {Set<number>}
     */
    const blocked = new Set()
    /**
     * @type {Set<Item|GC>}
     */
    const inPass = new Set()
    candidates.forEach(structs => structs.forEach(struct => inPass.add(struct)))
    /**
     * @param {ID|null} id
     * @return {number} 0: available, 1: might become available, 2: missing
     */
    const dependencyState = id => {
      if (id === null || this.docHas(id.client, id.clock)) {
        return 0
      }
      const struct = this.find(id)
      if (struct === null) {
        return 2
      }
      if (this.integrable.has(struct)) {
        return 0
      }
      return inPass.has(struct) && !blocked.has(id.client) ? 1 : 2
    }
    /**
     * @type {Map<number,number>}
     */
    const next = new Map()
    for (let progress = true; progress;) {
      progress = false
      for (const client of clients) {
        const structs = /** @type {Array<Item|GC>} */ (candidates.get(client))
        let i = next.get(client) ?? 0
        for (; !blocked.has(client) && i < structs.length; i++) {
          const struct = structs[i]
          const state = struct instanceof Item
            ? Math.max(dependencyState(struct.origin), dependencyState(struct.rightOrigin), dependencyState(struct.parent instanceof ID ? struct.parent : null))
            : 0
          if (state === 2) {
            blocked.add(client)
          }
          if (state !== 0) {
            break
          }
          this.integrable.add(struct)
          progress = true
        }
        next.set(client, i)
      }
    }
    clients.forEach(client => {
      if ((next.get(client) ?? 0) < /** @type {Array<Item|GC>} */ (candidates.get(client)).length) {
        blocked.add(client)
      }
    })
    return blocked
  }

  /**
   * Computes the parent that `Item.getMissing` is going to assign to an integrable item.
   *
   * @param {Item} item
   * @return {ResolvedParent|null} null if the item is going to be garbage collected
   */
  resolveParent (item) {
    /**
     * @type {Array<Item>}
     */
    const path = []
    /**
     * @type {ResolvedParent|null|undefined}
     */
    let res
    let current = item
    while (res === undefined) {
      res = this.parents.get(current)
      if (res !== undefined) {
        break
      }
      path.push(current)
      const parent = current.parent
      if (typeof parent === 'string') {
        res = { parent, parentSub: current.parentSub }
      } else if (parent instanceof ID) {
        const p = this.get(parent)
        res = p !== null && p.struct instanceof Item && p.struct.content instanceof ContentType
          ? { parent: p.inDoc ? p.struct.content.type : parent, parentSub: current.parentSub }
          : null
      } else {
        const left = current.origin === null ? null : this.get(current.origin)
        const right = current.rightOrigin === null ? null : this.get(current.rightOrigin)
        const next = left?.struct instanceof GC || right?.struct instanceof GC
          ? null
          : (left?.struct instanceof Item ? left : (right?.struct instanceof Item ? right : null))
        if (next === null) {
          res = null
        } else if (next.inDoc) {
          const nextItem = /** @type {Item} */ (next.struct)
          res = { parent: /** @type {YType} */ (nextItem.parent), parentSub: nextItem.parentSub }
        } else {
          current = /** @type {Item} */ (next.struct)
        }
      }
    }
    path.forEach(p => this.parents.set(p, res ?? null))
    return res ?? null
  }

  /**
   * Children of deleted types are deleted as a side effect. These deletes are not map writes.
   *
   * @param {YType|ID|string} parent
   * @param {IdSet} deletes
   */
  isParentDeleted (parent, deletes) {
    if (typeof parent === 'string') {
      return false
    }
    const id = parent instanceof ID ? parent : parent._item?.id
    if (id == null) {
      return false
    }
    const p = this.get(id)
    return deletes.hasId(id) || (p !== null && p.struct.deleted)
  }
}

class UpdateKeyWrites {
  /**
   * @param {YType|ID|string} parent
   * @param {string} parentId
   * @param {string} key
   */
  constructor (parent, parentId, key) {
    this.parent = parent
    this.parentId = parentId
    this.key = key
    /**
     * Items of this key that are integrated by the update.
     */
    this.tree = new KeyTree()
    /**
     * @type {Array<MapWrite>}
     */
    this.writes = []
    /**
     * Items of this key that are deleted by the update.
     *
     * @type {Array<{ client: number, clock: number, item: Item }>}
     */
    this.deleted = []
  }
}

/**
 * Detects map writes of a remote update before it is integrated. With `mapConflictPolicy: 'error'`,
 * the whole update is rejected if it contains conflicting writes.
 *
 * A remote delete only counts as a map write if it removes the value of the key. Deletes of items
 * that are overwritten (sequentially or concurrently) are part of the conflict resolution.
 *
 * @param {Transaction} transaction
 * @param {BlockSet} blocks Structs of the update that are not yet known
 * @param {IdSet} ds Delete set of the update
 */
export const trackRemoteMapWrites = (transaction, blocks, ds) => {
  const doc = transaction.doc
  const policy = doc.mapConflictPolicy
  if (policy === 'allow') {
    return
  }
  const store = doc.store
  const structs = new UpdateStructs(store)
  const incoming = structs.add(blocks)
  const postponed = structs.integrate(incoming)
  /**
   * @param {number} client
   */
  const stateAfterIntegration = client => (incoming.get(client) ?? []).reduce((state, struct) => structs.integrable.has(struct) ? math.max(state, struct.id.clock + struct.length) : state, getState(store, client))
  const pending = store.pendingStructs
  // readUpdateV2 retries the pending structs (merged with the postponed structs of this update) in
  // the same transaction if this update unblocks them
  if (pending !== null && array.from(pending.missing).some(([client, clock]) => (blocks.clients.has(client) && !postponed.has(client)) || clock < stateAfterIntegration(client))) {
    const retried = structs.add(readBlockSet(new UpdateDecoderV2(decoding.createDecoder(pending.update))))
    incoming.forEach((added, client) => {
      const rest = added.filter(struct => !structs.integrable.has(struct))
      if (rest.length > 0) {
        map.setIfUndefined(retried, client, () => /** @type {Array<Item|GC>} */ ([])).push(...rest)
      }
    })
    retried.forEach(list => list.sort((a, b) => a.id.clock - b.id.clock))
    structs.integrate(retried)
  }
  let deletes = ds
  if (store.pendingDs !== null) {
    const decoder = new UpdateDecoderV2(decoding.createDecoder(store.pendingDs))
    decoding.readVarUint(decoder.restDecoder) // pending deletes are encoded without structs
    deletes = mergeIdSets([ds, readIdSet(decoder)])
  }
  /**
   * @type {Map<string,Map<string,UpdateKeyWrites>>}
   */
  const groups = new Map()
  /**
   * @param {YType|ID|string} parent
   * @param {string} key
   */
  const getGroup = (parent, key) => {
    const parentId = getMapParentId(parent)
    const keys = map.setIfUndefined(groups, parentId, () => /** @type {Map<string,UpdateKeyWrites>} */ (new Map()))
    return map.setIfUndefined(keys, key, () => new UpdateKeyWrites(parent, parentId, key))
  }
  const clients = array.from(structs.clients.keys()).sort((a, b) => a - b)
  clients.forEach(client => {
    /** @type {Array<Item|GC>} */ (structs.clients.get(client)).forEach(struct => {
      const resolved = struct instanceof Item && structs.integrable.has(struct) ? structs.resolveParent(struct) : null
      if (resolved == null || resolved.parentSub === null) {
        return
      }
      const group = getGroup(resolved.parent, resolved.parentSub)
      const item = /** @type {Item} */ (struct)
      for (let j = 0; j < item.length; j++) {
        const clock = item.id.clock + j
        if (structs.docHas(client, clock)) {
          continue
        }
        group.tree.add(client, clock, j === 0 ? item.origin : createID(client, clock - 1))
        // garbage collected values are history that was already resolved by the sender
        if (item.content.constructor !== ContentDeleted) {
          group.writes.push(createMapWrite('set', group.parentId, group.key, client, clock, 'remote', item.content, j))
        }
      }
    })
  })
  array.from(deletes.clients.entries()).sort((a, b) => a[0] - b[0]).forEach(([client, ranges]) => {
    const docStructs = store.clients.get(client) ?? []
    const state = getState(store, client)
    const clientStructs = structs.clients.get(client) ?? []
    ranges.getIds().forEach(({ clock, len }) => {
      const end = clock + len
      for (let c = clock; c < math.min(end, state);) {
        const struct = docStructs[findIndexSS(docStructs, c)]
        if (struct instanceof Item && !struct.deleted && struct.parentSub !== null && !structs.isParentDeleted(/** @type {YType} */ (struct.parent), deletes)) {
          const group = getGroup(/** @type {YType} */ (struct.parent), struct.parentSub)
          for (let k = math.max(c, struct.id.clock); k < math.min(end, struct.id.clock + struct.length); k++) {
            group.deleted.push({ client, clock: k, item: struct })
          }
        }
        c = struct.id.clock + struct.length
      }
      for (let i = lowerBound(clientStructs, clock); i < clientStructs.length && clientStructs[i].id.clock < end; i++) {
        const struct = clientStructs[i]
        const resolved = struct instanceof Item && structs.integrable.has(struct) ? structs.resolveParent(struct) : null
        if (resolved == null || resolved.parentSub === null || structs.isParentDeleted(resolved.parent, deletes)) {
          continue
        }
        const group = getGroup(resolved.parent, resolved.parentSub)
        for (let k = math.max(clock, struct.id.clock); k < math.min(end, struct.id.clock + struct.length); k++) {
          if (!structs.docHas(client, k)) {
            group.deleted.push({ client, clock: k, item: /** @type {Item} */ (struct) })
          }
        }
      }
    })
  })
  const log = getMapWriteLog(transaction)
  /**
   * @type {Array<MapConflict>}
   */
  const conflicts = []
  /**
   * @type {Array<{ parent: YType|ID|string, writes: Array<MapWrite> }>}
   */
  const additions = []
  groups.forEach(keys => keys.forEach(group => {
    /**
     * @type {KeyNode|null|undefined}
     */
    let rightmost
    const getRightmost = () => {
      if (rightmost === undefined) {
        group.tree.addChain(getParentType(doc, group.parent)?._map.get(group.key))
        rightmost = group.tree.rightmost()
      }
      return rightmost
    }
    if (group.deleted.length > 0) {
      // without new items, the only live item of the key is the one that holds its value
      const target = group.tree.nodes.size === 0 ? group.deleted[0] : group.deleted.find(({ client, clock }) => client === getRightmost()?.client && clock === getRightmost()?.clock)
      if (target !== undefined) {
        group.writes.push(createMapWrite('delete', group.parentId, group.key, target.client, target.clock, 'remote', target.item.content, target.clock - target.item.id.clock))
      }
    }
    const fresh = group.writes.filter(write => !log.has(write))
    if (fresh.length === 0) {
      return
    }
    additions.push({ parent: group.parent, writes: fresh })
    if (policy === 'error') {
      const writes = log.get(group.parentId, group.key).concat(fresh)
      const kind = classifyMapWrites(writes)
      if (kind !== null) {
        const winner = getRightmost()
        const winnerDeleted = winner !== null && (
          writes.some(write => write.op === 'delete' && write.client === winner.client && write.clock === winner.clock) ||
          (structs.docHas(winner.client, winner.clock) && getItem(store, createID(winner.client, winner.clock)).deleted)
        )
        conflicts.push(createMapConflict(group.parentId, group.key, kind, writes, resolveMapConflict(writes, group.tree, winner, winnerDeleted, (client, clock) => {
          const s = structs.get(createID(client, clock))
          return s !== null && s.struct instanceof Item ? describeItemValue(s.struct, clock, group.parentId, group.key, s.inDoc ? 'existing' : 'remote') : null
        })))
      }
    }
  }))
  if (conflicts.length > 0) {
    throw new MapConflictError(conflicts)
  }
  additions.forEach(({ parent, writes }) => writes.forEach(write => log.add(parent, write)))
}

/**
 * Records the conflicts of a finished transaction if `mapConflictPolicy` is "collect".
 *
 * @param {Transaction} transaction
 */
export const collectMapConflicts = transaction => {
  const doc = transaction.doc
  const log = transaction._mapWrites
  if (log === null || doc.mapConflictPolicy !== 'collect') {
    return
  }
  log.groups.forEach(keys => keys.forEach(({ parent, parentId, key, writes }) => {
    const kind = classifyMapWrites(writes)
    if (kind === null) {
      return
    }
    const item = getParentType(doc, parent)?._map.get(key) ?? null
    const tree = new KeyTree()
    tree.addChain(item)
    const rightmost = item === null ? null : { client: item.id.client, clock: item.id.clock + item.length - 1 }
    const resolution = resolveMapConflict(writes, tree, rightmost, item !== null && item.deleted, (client, clock) => {
      if (item === null) {
        return null
      }
      const inserted = transaction.insertSet.has(client, clock)
      return describeItemValue(item, clock, parentId, key, inserted ? (client === doc.clientID ? 'local' : 'remote') : 'existing')
    })
    doc._mapConflicts.push(createMapConflict(parentId, key, kind, writes, resolution))
  }))
}

/**
 * @param {{[key:string]:number}} counts
 * @param {string} key
 */
const increment = (counts, key) => {
  if (object.hasProperty(counts, key)) {
    counts[key]++
  } else {
    // defineProperty also supports keys like "__proto__"
    Object.defineProperty(counts, key, { value: 1, writable: true, enumerable: true, configurable: true })
  }
}

/**
 * @param {Array<MapConflict>} conflicts
 * @return {MapConflictSummary}
 */
export const summarizeMapConflicts = conflicts => {
  /**
   * @type {MapConflictSummary}
   */
  const summary = { count: conflicts.length, total: conflicts.length, byType: {}, byKey: {}, byParent: {}, bySource: {} }
  conflicts.forEach(conflict => {
    increment(summary.byType, conflict.type)
    increment(summary.byKey, conflict.key)
    increment(summary.byParent, conflict.parentId)
    increment(summary.bySource, conflict.source)
  })
  return summary
}
