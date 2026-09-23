import type { Relation } from '../relation/types';
import type { Trait } from '../trait/types';

/** A trait or relation registered under a stable string key. */
export type TraitRegistryEntry = Trait | Relation<Trait>;

/**
 * Maps stable string keys to traits and relations.
 * Snapshots store these keys instead of live trait identities.
 */
export type TraitRegistry = {
    get(key: string): TraitRegistryEntry | undefined;
    has(key: string): boolean;
    keys(): string[];
};

export type RelationSnapshotLink = {
    targetId: number;
    data?: object;
};

export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
    relations?: Record<string, RelationSnapshotLink[]>;
};

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
