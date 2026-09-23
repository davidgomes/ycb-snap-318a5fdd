import type { Entity } from '../entity/types';
import type { Relation, RelationPair, RelationTarget } from '../relation/types';
import type { ConfigurableTrait, Trait } from '../trait/types';
import type { DeferredView } from './view';

export type DeferredCommands = {
    /** Reserves an entity now and spawns it with the given traits when the buffer executes. */
    spawn(...traits: ConfigurableTrait[]): Entity;
    destroy(entity: Entity): void;
    add(entity: Entity, ...traits: ConfigurableTrait[]): void;
    remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void;
    /** Replaces all pairs of the relation with this one. A `'*'` target clears all pairs. */
    addExclusive(entity: Entity, pair: RelationPair): void;
    /** Executes the commands of the current scope. */
    flush(): void;
};

export type TraitEntry = { kind: 'trait'; trait: Trait; params: any };

export type PairEntry = {
    kind: 'pair';
    relation: Relation<Trait>;
    target: RelationTarget;
    params: Record<string, unknown> | undefined;
};

export type DeferredEntry = TraitEntry | PairEntry;

export type DeferredCommand =
    | { type: 'spawn'; entity: Entity; entries: DeferredEntry[] }
    | { type: 'add'; entity: Entity; entries: DeferredEntry[] }
    | { type: 'remove'; entity: Entity; entries: DeferredEntry[] }
    | { type: 'addExclusive'; entity: Entity; entry: PairEntry }
    | { type: 'destroy'; entity: Entity };

export type PendingAdds = {
    traits: Map<Trait, TraitEntry>;
    pairs: Map<Relation<Trait>, Map<Entity, PairEntry>>;
};

export type DeferredBuffer = {
    commands: DeferredCommand[];
    /** Entities that are the subject of a command in this buffer. */
    entities: Set<Entity>;
    spawned: Set<Entity>;
    /** Entities spawned and destroyed in this buffer. Neither command executes. */
    nullified: Set<Entity>;
    /** Adds that will still be in effect when the buffer executes, so later values can replace them. */
    pendingAdds: Map<Entity, PendingAdds>;
};

export type DeferredState = {
    /** Buffer scopes. Index 0 is the root scope, the last is the current scope. */
    stack: DeferredBuffer[];
    pool: DeferredBuffer[];
    /** Entities reserved by a deferred spawn that has not executed yet. */
    reserved: Set<Entity>;
    /** Number of commands pending across all scopes. */
    pending: number;
    version: number;
    view: DeferredView | null;
    viewVersion: number;
};
