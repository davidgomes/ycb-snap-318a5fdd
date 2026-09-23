import type { Relation } from '../relation/types';
import type { Trait } from '../trait/types';

/** A named catalog of traits and relations that snapshots are allowed to touch. */
export type TraitRegistry = {
    /** Resolve a registry key to the trait or relation it names. */
    get(key: string): Trait | Relation<Trait> | undefined;
    /** Registry key for a trait, if that trait was registered. */
    keyForTrait(trait: Trait): string | undefined;
    /** Registry key for a relation, if that relation was registered. */
    keyForRelation(relation: Relation<Trait>): string | undefined;
};

/** One relation target captured on an entity snapshot. */
export type RelationSnapshot = {
    targetId: number;
    /** Present only when the relation was created with a store. Deep-copied. */
    data?: object;
};

/**
 * Point-in-time copy of one entity.
 * `relations` is omitted entirely when the entity has no relations.
 */
export type EntitySnapshot = {
    /** `entity.id()`, the stable id slot — not the packed entity number. */
    id: number;
    /** Tag traits are `true`. Data traits are deep copies. */
    traits: Record<string, object | true>;
    relations?: Record<string, RelationSnapshot[]>;
};

/** Point-in-time copy of every non-world entity. */
export type WorldSnapshot = {
    entities: EntitySnapshot[];
};

/** Trait-level diff of two entity snapshots, from `a` to `b`. */
export type EntitySnapshotDiff = {
    addedTraits: string[];
    removedTraits: string[];
    changedTraits: string[];
};

/** Entity-level diff of two world snapshots, from `before` to `after`. */
export type WorldSnapshotDiff = {
    added: number[];
    removed: number[];
    changed: number[];
};
