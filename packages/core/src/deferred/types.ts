import type { Entity } from '../entity/types';
import type { Relation, RelationPair } from '../relation/types';
import type { ConfigurableTrait, Trait } from '../trait/types';

export type DeferredCommand =
    | { kind: 'spawn'; entity: Entity; traits: ConfigurableTrait[] }
    | { kind: 'destroy'; entity: Entity }
    | { kind: 'add'; entity: Entity; traits: ConfigurableTrait[] }
    | { kind: 'remove'; entity: Entity; traits: (Trait | RelationPair)[] }
    | { kind: 'addExclusive'; entity: Entity; pair: RelationPair };

export type DeferredBuffer = {
    commands: DeferredCommand[];
    /** Entities with at least one queued command. */
    entities: Set<Entity>;
    /** Entities whose spawn is still queued in this buffer. */
    spawned: Set<Entity>;
    /** Spawn+destroy pairs cancelled in this buffer. Later commands are ignored. */
    nullified: Set<Entity>;
};

export function createDeferredBuffer(): DeferredBuffer {
    return {
        commands: [],
        entities: new Set(),
        spawned: new Set(),
        nullified: new Set(),
    };
}

/** Commands queued on `world.deferred` run later, in the order they were recorded. */
export type Deferred = {
    spawn(...traits: ConfigurableTrait[]): Entity;
    destroy(entity: Entity): void;
    add(entity: Entity, ...traits: ConfigurableTrait[]): void;
    remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void;
    addExclusive(entity: Entity, pair: RelationPair): void;
    flush(): void;
};

export type DeferredRead =
    | { hit: false }
    | { hit: true; value: unknown };

export type DeferredHooks = {
    beforeMutation: ((entity: Entity) => void) | null;
    enterDeferred: (() => void) | null;
    exitDeferred: (() => void) | null;
    captureSnapshot: ((entity: Entity) => void) | null;
    readHas: ((entity: Entity, trait: Trait | RelationPair) => boolean | null) | null;
    readGet: ((entity: Entity, trait: Trait | RelationPair) => DeferredRead | null) | null;
    readTargets: ((entity: Entity, relation: Relation) => readonly Entity[] | null) | null;
    /** `null` when this entity has no queued commands and the caller should use live state. */
    readAlive: ((entity: Entity) => boolean | null) | null;
};
