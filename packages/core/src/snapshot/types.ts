import type { $internal } from '../common';
import type { Relation } from '../relation/types';
import type { Trait } from '../trait/types';

export type TraitRegistryEntry = [key: string, trait: Trait | Relation<Trait>];

export type TraitRegistry = {
    readonly [$internal]: {
        entries: Map<string, Trait | Relation<Trait>>;
        keys: Map<Trait | Relation<Trait>, string>;
    };
};

export type RelationTargetSnapshot = {
    targetId: number;
    data?: object;
};

export type EntitySnapshot = {
    /** The entity ID and generation, without the world. */
    id: number;
    /** Tags are stored as `true`, data traits as deep copies. */
    traits: Record<string, object | true>;
    /** Omitted when the entity has no relations. */
    relations?: Record<string, RelationTargetSnapshot[]>;
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
