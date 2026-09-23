import type { Entity } from '../entity/types';
import type { Relation, RelationPair } from '../relation/types';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { World } from '../world';

export type Deferred = {
    /** Reserves an entity that is spawned with the traits when the commands execute. */
    spawn(...traits: ConfigurableTrait[]): Entity;
    destroy(entity: Entity): void;
    add(entity: Entity, ...traits: ConfigurableTrait[]): void;
    remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void;
    /** Replaces all pairs of the relation with this pair. A wildcard target removes all pairs. */
    addExclusive(entity: Entity, pair: RelationPair): void;
    /** Executes the commands of the current scope. */
    flush(): void;
};

export type DeferredOp =
    | { type: 'add'; trait: Trait; value: unknown; hasParams: boolean }
    | { type: 'remove'; trait: Trait }
    | {
          type: 'addPair' | 'exclusivePair';
          relation: Relation<Trait>;
          target: Entity;
          value: unknown;
          hasParams: boolean;
      }
    | { type: 'removePair'; relation: Relation<Trait>; target: Entity }
    | { type: 'clearPairs'; relation: Relation<Trait> };

export type DeferredCommand =
    | { type: 'spawn' | 'mutate'; entity: Entity; ops: DeferredOp[] }
    | { type: 'destroy'; entity: Entity };

/** The state an entity will have once the pending commands execute. */
export type DeferredView = {
    entity: Entity;
    alive: boolean;
    /** Whether the entity was alive in the world when the simulation started. */
    materialized: boolean;
    traits: Map<Trait, DeferredTraitSlot>;
    relations: Map<Relation<Trait>, DeferredRelationSlot>;
};

export type DeferredTraitSlot = {
    view: DeferredView;
    trait: Trait;
    present: boolean;
    /** Whether `value` comes from a command rather than the world. */
    buffered: boolean;
    value: unknown;
};

export type DeferredPair = { buffered: boolean; value: unknown };

export type DeferredRelationSlot = {
    view: DeferredView;
    relation: Relation<Trait>;
    targets: Map<Entity, DeferredPair>;
};

export type DeferredEffect =
    | { type: 'spawn' | 'destroy'; view: DeferredView }
    | { type: 'trait'; slot: DeferredTraitSlot }
    | { type: 'relation'; slot: DeferredRelationSlot };

export type DeferredSimulation = {
    world: World;
    views: Map<Entity, DeferredView>;
    /** Net changes in the order they were first touched. */
    effects: DeferredEffect[];
    relationSlots: Map<Relation<Trait>, DeferredRelationSlot[]>;
    hasDestroys: boolean;
    /** Set when a command tried to destroy the world entity. */
    aborted: boolean;
};

export type DeferredInternal = {
    /** The root buffer followed by one buffer per active `updateEach` scope. */
    buffers: DeferredCommand[][];
    /** Pending commands across all buffers. */
    size: number;
    /** Pending destroy commands across all buffers. */
    destroys: number;
    /** Pending command count per entity the commands act on. */
    subjects: Map<Entity, number>;
    /** Entities reserved by pending spawns. */
    reserved: Set<Entity>;
    /** Cached simulation of every pending buffer, used to answer `has` and `get`. */
    view: DeferredSimulation | null;
};
