import type { $internal } from '../common';
import type { Relation } from '../relation/types';
import type { Trait } from '../trait/types';

export type TraitRegistryEntry = readonly [string, Trait | Relation<Trait>];

export type TraitRegistry = {
    readonly keys: readonly string[];
    [$internal]: {
        entries: Map<string, Trait | Relation<Trait>>;
        traitKeys: Map<Trait, string>;
        relationKeys: Map<Relation<Trait>, string>;
    };
};

export type RelationTargetSnapshot = {
    targetId: number;
    data?: object;
};

export type EntitySnapshot = {
    id: number;
    traits: Record<string, object | true>;
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
