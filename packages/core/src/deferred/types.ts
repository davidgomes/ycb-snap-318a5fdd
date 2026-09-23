import type { Entity } from '../entity/types';
import type { Relation, RelationPair } from '../relation/types';
import type { ConfigurableTrait, Trait } from '../trait/types';

export type DeferredCommands = {
    spawn(...traits: ConfigurableTrait[]): Entity;
    destroy(entity: Entity): void;
    add(entity: Entity, ...traits: ConfigurableTrait[]): void;
    remove(entity: Entity, ...traits: (Trait | RelationPair)[]): void;
    /** Replaces all pairs of the relation with this one. A `'*'` target clears all pairs. */
    addExclusive(entity: Entity, pair: RelationPair): void;
    flush(): void;
};

type BaseCommand = {
    entity: Entity;
    /** False once a later command supersedes this one. */
    alive: boolean;
};

export type SpawnCommand = BaseCommand & { type: 'spawn' };

export type DestroyCommand = BaseCommand & { type: 'destroy' };

export type AddCommand = BaseCommand & {
    type: 'add';
    trait: Trait;
    params: unknown;
    /** The trait was removed earlier in the buffer, so an existing value is overwritten. */
    reset: boolean;
    resolved: boolean;
    value: unknown;
};

export type RemoveCommand = BaseCommand & { type: 'remove'; trait: Trait };

export type AddPairCommand = BaseCommand & {
    type: 'addPair';
    relation: Relation<Trait>;
    target: Entity;
    params: Record<string, unknown> | undefined;
    /** The pair was removed earlier in the buffer, so an existing value is overwritten. */
    reset: boolean;
    resolved: boolean;
    value: Record<string, unknown> | undefined;
};

export type RemovePairCommand = BaseCommand & {
    type: 'removePair';
    relation: Relation<Trait>;
    target: Entity;
};

export type ClearPairsCommand = BaseCommand & {
    type: 'clearPairs';
    relation: Relation<Trait>;
    /** Target that survives the clear, used when adding exclusively. */
    except: Entity | null;
};

export type Command =
    | SpawnCommand
    | DestroyCommand
    | AddCommand
    | RemoveCommand
    | AddPairCommand
    | RemovePairCommand
    | ClearPairsCommand;

export type RelationRecord = {
    clear: ClearPairsCommand | null;
    /** Live pair commands, all issued after `clear`. */
    pairs: Map<Entity, AddPairCommand | RemovePairCommand>;
};

export type EntityRecord = {
    spawn: SpawnCommand | null;
    destroyed: boolean;
    traits: Map<Trait, AddCommand | RemoveCommand>;
    relations: Map<Relation<Trait>, RelationRecord>;
};

export type CommandBuffer = {
    commands: Command[];
    records: Map<Entity, EntityRecord>;
    pairAddsByTarget: Map<Entity, Set<AddPairCommand>>;
};
