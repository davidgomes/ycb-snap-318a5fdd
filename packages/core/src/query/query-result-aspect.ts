import { isAspect, type Aspect } from '../aspect/aspect';
import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import type { Relation } from '../relation/types';
import { isRelationPair } from '../relation/utils/is-relation';
import type { Store } from '../storage';
import { getStore } from '../trait/trait';
import type { Trait } from '../trait/types';
import { shallowEqual } from '../utils/shallow-equal';
import type { World } from '../world';
import { isModifier } from './modifier';
import { setChanged } from './modifiers/changed';
import type {
    InstancesFromParameters,
    QueryInstance,
    QueryParameter,
    QueryResult,
    QueryResultOptions,
} from './types';

type Part = { trait: Trait; store: Store<any>; keys: string[] };

type Column =
    | { kind: 'trait'; trait: Trait; store: Store<any> }
    | { kind: 'aspect'; parts: Part[] };

export function parametersHaveAspect(params: readonly QueryParameter[]): boolean {
    for (let i = 0; i < params.length; i++) {
        const param = params[i];
        if (isAspect(param)) return true;
        if (isModifier(param) && param.aspectGroups && param.aspectGroups.length > 0) return true;
    }
    return false;
}

function pushTrait(columns: Column[], world: World, trait: Trait) {
    if (trait[$internal].type === 'tag') return;
    columns.push({ kind: 'trait', trait, store: getStore(world, trait) });
}

function pushAspect(columns: Column[], world: World, aspect: Aspect) {
    const parts: Part[] = [];
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const keys = aspect[$internal].keysByTrait.get(trait) ?? [];
        if (trait[$internal].type === 'tag' || keys.length === 0) continue;
        parts.push({ trait, store: getStore(world, trait), keys });
    }
    columns.push({ kind: 'aspect', parts });
}

function buildColumns(params: readonly QueryParameter[], world: World): Column[] {
    const columns: Column[] = [];

    for (let i = 0; i < params.length; i++) {
        const param = params[i];

        if (isRelationPair(param)) {
            const relation = param[$internal].relation as Relation<Trait>;
            const baseTrait = relation[$internal].trait;
            pushTrait(columns, world, baseTrait);
            continue;
        }

        if (isAspect(param)) {
            pushAspect(columns, world, param);
            continue;
        }

        if (isModifier(param)) {
            if (param.type === 'not') continue;

            const terms = param.terms;
            if (terms && terms.length > 0) {
                for (let t = 0; t < terms.length; t++) {
                    const term = terms[t];
                    if (term.kind === 'aspect') pushAspect(columns, world, term.aspect);
                    else pushTrait(columns, world, term.trait);
                }
                continue;
            }

            const aspects = param.aspectGroups ?? [];
            const grouped = new Set<Trait>();
            for (let a = 0; a < aspects.length; a++) {
                const aspect = aspects[a];
                pushAspect(columns, world, aspect);
                for (let t = 0; t < aspect.traits.length; t++) grouped.add(aspect.traits[t]);
            }

            const traits = param.traits;
            for (let t = 0; t < traits.length; t++) {
                if (grouped.has(traits[t])) continue;
                pushTrait(columns, world, traits[t]);
            }
            continue;
        }

        pushTrait(columns, world, param as Trait);
    }

    return columns;
}

function readColumn(column: Column, eid: number): any {
    if (column.kind === 'trait') return column.trait[$internal].get(eid, column.store);

    const merged: Record<string, unknown> = {};
    const parts = column.parts;
    for (let i = 0; i < parts.length; i++) {
        const part = parts[i];
        const value = part.trait[$internal].get(eid, part.store);
        for (let k = 0; k < part.keys.length; k++) {
            const key = part.keys[k];
            merged[key] = value[key];
        }
    }
    return merged;
}

function writePart(
    part: Part,
    eid: number,
    source: Record<string, unknown>,
    detect: boolean
): boolean {
    const partial: Record<string, unknown> = {};
    for (let k = 0; k < part.keys.length; k++) {
        const key = part.keys[k];
        partial[key] = source[key];
    }

    const ctx = part.trait[$internal];
    if (!detect) {
        ctx.fastSet(eid, part.store, partial);
        return false;
    }

    return ctx.fastSetWithChangeDetection(eid, part.store, partial);
}

function columnIsTracked(column: Column, world: World, query: QueryInstance): boolean {
    if (column.kind === 'trait') {
        return (
            world[$internal].trackedTraits.has(column.trait) ||
            (query.hasChangedModifiers && query.changedTraits.has(column.trait))
        );
    }

    for (let i = 0; i < column.parts.length; i++) {
        const trait = column.parts[i].trait;
        if (world[$internal].trackedTraits.has(trait)) return true;
        if (query.hasChangedModifiers && query.changedTraits.has(trait)) return true;
    }
    return false;
}

export function createMergedQueryResult<T extends QueryParameter[]>(
    world: World,
    entities: Entity[],
    query: QueryInstance,
    params: QueryParameter[]
): QueryResult<T> {
    let columns = buildColumns(params, world);

    const results = Object.assign(entities, {
        readEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void
        ) {
            const state = Array.from({ length: columns.length }) as InstancesFromParameters<T>;

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);
                for (let c = 0; c < columns.length; c++) state[c] = readColumn(columns[c], eid);
                callback(state, entity, i);
            }

            return results;
        },

        updateEach(
            callback: (state: InstancesFromParameters<T>, entity: Entity, index: number) => void,
            options: QueryResultOptions = { changeDetection: 'auto' }
        ) {
            const mode = options.changeDetection ?? 'auto';
            const state = Array.from({ length: columns.length });
            const changedPairs: [Entity, Trait][] = [];
            const atomicSnapshots: any[] = [];

            const tracked: boolean[] = [];
            if (mode === 'auto') {
                for (let c = 0; c < columns.length; c++) {
                    tracked[c] = columnIsTracked(columns[c], world, query);
                }
            }

            for (let i = 0; i < entities.length; i++) {
                const entity = entities[i];
                const eid = getEntityId(entity);

                for (let c = 0; c < columns.length; c++) {
                    const column = columns[c];
                    const value = readColumn(column, eid);
                    state[c] = value;
                    if (column.kind === 'trait' && column.trait[$internal].type === 'aos') {
                        atomicSnapshots[c] = { ...value };
                    } else {
                        atomicSnapshots[c] = null;
                    }
                }

                callback(state as unknown as InstancesFromParameters<T>, entity, i);
                if (!world.has(entity)) continue;

                for (let c = 0; c < columns.length; c++) {
                    const column = columns[c];
                    const detect = mode === 'always' || (mode === 'auto' && tracked[c]);

                    if (column.kind === 'trait') {
                        if (!detect) {
                            column.trait[$internal].fastSet(eid, column.store, state[c]);
                            continue;
                        }

                        const ctx = column.trait[$internal];
                        let changed = ctx.fastSetWithChangeDetection(eid, column.store, state[c]);
                        if (!changed && ctx.type === 'aos') {
                            changed = !shallowEqual(state[c], atomicSnapshots[c]);
                        }
                        if (changed) changedPairs.push([entity, column.trait]);
                        continue;
                    }

                    const source = state[c] as Record<string, unknown>;
                    for (let p = 0; p < column.parts.length; p++) {
                        const part = column.parts[p];
                        const partDetect =
                            mode === 'always' ||
                            (mode === 'auto' &&
                                (world[$internal].trackedTraits.has(part.trait) ||
                                    (query.hasChangedModifiers && query.changedTraits.has(part.trait))));
                        if (writePart(part, eid, source, mode === 'never' ? false : partDetect || detect)) {
                            changedPairs.push([entity, part.trait]);
                        }
                    }
                }
            }

            if (mode !== 'never') {
                for (let i = 0; i < changedPairs.length; i++) {
                    const [entity, trait] = changedPairs[i];
                    setChanged(world, entity, trait);
                }
            }

            return results;
        },

        useStores(callback: (stores: any, entities: readonly Entity[]) => void) {
            const stores: Store<any>[] = [];
            for (let c = 0; c < columns.length; c++) {
                const column = columns[c];
                if (column.kind === 'trait') stores.push(column.store);
                else for (let p = 0; p < column.parts.length; p++) stores.push(column.parts[p].store);
            }
            callback(stores, entities);
            return results;
        },

        select<U extends QueryParameter[]>(...next: U): QueryResult<U> {
            columns = buildColumns(next, world);
            return results as unknown as QueryResult<U>;
        },

        sort(
            callback: (a: Entity, b: Entity) => number = (a, b) => getEntityId(a) - getEntityId(b)
        ): QueryResult<T> {
            Array.prototype.sort.call(entities, callback);
            return results;
        },
    });

    return results;
}
