import type { Relation } from '../relation/types';
import type { Trait } from '../trait/types';

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

export type TraitRegistryEntry = Trait | Relation<Trait>;

export type TraitRegistry = {
    entries: Map<string, TraitRegistryEntry>;
    traitKeys: Map<Trait, string>;
    relationKeys: Map<Relation<Trait>, string>;
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
