import type { Relation } from '../relation/types';
import type { Trait } from '../trait/types';

export type TraitRegistryEntry = readonly [string, Trait | Relation<any>];

export type TraitRegistry = {
    readonly entries: readonly TraitRegistryEntry[];
    readonly byKey: ReadonlyMap<string, Trait | Relation<Trait>>;
    readonly traitKeys: ReadonlyMap<Trait, string>;
    readonly relationKeys: ReadonlyMap<Relation<Trait>, string>;
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
