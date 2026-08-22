import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { getEntityId } from '../entity/utils/pack-entity';
import { getRelationData, getRelationTargets } from '../relation/relation';
import { isRelation } from '../relation/utils/is-relation';
import { getTrait, hasTrait } from '../trait/trait';
import type { Trait } from '../trait/types';
import type { World } from '../world';
import type { Relation } from '../relation/types';
import type { EntitySnapshot, RelationTargetSnapshot, TraitRegistry } from './types';

export function shallowEqual(a: object | undefined, b: object | undefined): boolean {
    if (a === b) return true;
    if (a === undefined || b === undefined) return a === b;
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    if (keysA.length !== keysB.length) return false;
    for (const key of keysA) {
        if ((a as Record<string, unknown>)[key] !== (b as Record<string, unknown>)[key]) {
            return false;
        }
    }
    return true;
}

export function traitValuesEqual(a: object | true, b: object | true): boolean {
    if (a === true && b === true) return true;
    if (a === true || b === true) return false;
    return shallowEqual(a, b);
}

export function getRegistryKeyForTrait(registry: TraitRegistry, trait: Trait): string | undefined {
    const direct = registry.traitKeys.get(trait);
    if (direct !== undefined) return direct;

    const relation = trait[$internal].relation;
    if (relation) return registry.relationKeys.get(relation);

    return undefined;
}

export function assertEntityAlive(world: World, entity: Entity): void {
    if (!world.has(entity)) {
        throw new Error('Koota: Cannot snapshot or rollback a destroyed entity.');
    }
}

export function assertRegistryKey(registry: TraitRegistry, key: string): void {
    if (!registry.entries.has(key)) {
        throw new Error(`Koota: Unknown registry key "${key}".`);
    }
}

export function assertEntityTraitsRegistered(
    world: World,
    entity: Entity,
    registry: TraitRegistry
): void {
    const entityTraits = world[$internal].entityTraits.get(entity);
    if (!entityTraits) return;

    for (const trait of entityTraits) {
        if (!getRegistryKeyForTrait(registry, trait)) {
            throw new Error('Koota: Entity has an unregistered trait or relation.');
        }
    }
}

export function findEntityById(world: World, id: number): Entity | undefined {
    for (const entity of world.entities) {
        if (getEntityId(entity) === id) return entity;
    }
    return undefined;
}

export function snapshotTraitValue(
    world: World,
    entity: Entity,
    trait: Trait
): object | true {
    if (trait[$internal].type === 'tag') return true;
    return structuredClone(getTrait(world, entity, trait) as object);
}

export function snapshotRelationTargets(
    world: World,
    entity: Entity,
    relation: Relation<Trait>
): RelationTargetSnapshot[] {
    const relationTrait = relation[$internal].trait;
    const isTag = relationTrait[$internal].type === 'tag';
    const targets = getRelationTargets(world, relation, entity);

    return targets.map((target) => {
        const entry: RelationTargetSnapshot = { targetId: getEntityId(target) };
        if (!isTag) {
            const data = getRelationData(world, entity, relation, target);
            if (data !== undefined && typeof data === 'object') {
                entry.data = structuredClone(data as object);
            }
        }
        return entry;
    });
}

export function normalizeEntitySnapshot(snapshot: EntitySnapshot): EntitySnapshot {
    const relations =
        snapshot.relations && Object.keys(snapshot.relations).length > 0
            ? Object.fromEntries(
                  Object.entries(snapshot.relations).map(([key, targets]) => [
                      key,
                      [...targets].sort((a, b) => a.targetId - b.targetId),
                  ])
              )
            : undefined;

    return {
        id: snapshot.id,
        traits: { ...snapshot.traits },
        relations,
    };
}

export function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    const left = normalizeEntitySnapshot(a);
    const right = normalizeEntitySnapshot(b);

    const traitKeysLeft = Object.keys(left.traits).sort();
    const traitKeysRight = Object.keys(right.traits).sort();
    if (traitKeysLeft.length !== traitKeysRight.length) return false;
    for (let i = 0; i < traitKeysLeft.length; i++) {
        const key = traitKeysLeft[i]!;
        if (key !== traitKeysRight[i]) return false;
        if (!traitValuesEqual(left.traits[key]!, right.traits[key]!)) return false;
    }

    const relationKeysLeft = left.relations ? Object.keys(left.relations).sort() : [];
    const relationKeysRight = right.relations ? Object.keys(right.relations).sort() : [];
    if (relationKeysLeft.length !== relationKeysRight.length) return false;

    for (let i = 0; i < relationKeysLeft.length; i++) {
        const key = relationKeysLeft[i]!;
        if (key !== relationKeysRight[i]) return false;

        const targetsLeft = left.relations![key]!;
        const targetsRight = right.relations![key]!;
        if (targetsLeft.length !== targetsRight.length) return false;

        for (let j = 0; j < targetsLeft.length; j++) {
            const targetLeft = targetsLeft[j]!;
            const targetRight = targetsRight[j]!;
            if (targetLeft.targetId !== targetRight.targetId) return false;
            if (!shallowEqual(targetLeft.data, targetRight.data)) return false;
        }
    }

    return true;
}

export function collectSnapshotRegistryKeys(snapshot: EntitySnapshot): string[] {
    return [
        ...Object.keys(snapshot.traits),
        ...Object.keys(snapshot.relations ?? {}),
    ];
}
