import type { $internal } from '../common';

/** One relation target captured on an entity snapshot. */
export type RelationLinkSnapshot = {
    targetId: number;
    data?: object;
};

/**
 * Plain-data copy of one entity.
 * Tag traits are `true`. Data traits and relation payloads are deep copies.
 * `relations` is omitted when the entity has none.
 */
export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
    relations?: Record<string, RelationLinkSnapshot[]>;
};

/** Plain-data copy of every entity in a world except the internal world entity. */
export type WorldSnapshot = {
    entities: EntitySnapshot[];
};

export type EntitySnapshotDiff = {
    addedTraits: string[];
    removedTraits: string[];
    changedTraits: string[];
};

export type WorldSnapshotDiff = {
    added: number[];
    removed: number[];
    changed: number[];
};

/**
 * Names for the traits and relations a snapshot is allowed to read or write.
 * Built with `createTraitRegistry`.
 */
export type TraitRegistry = {
    readonly [$internal]: {
        byKey: ReadonlyMap<string, any>;
        keysByTrait: ReadonlyMap<any, string>;
        keysByRelation: ReadonlyMap<any, string>;
    };
};
