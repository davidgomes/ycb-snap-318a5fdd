import type { Entity } from '../entity/types';
import type { Relation } from '../relation/types';
import type { RelationPair } from '../relation/types';
import type { Trait } from '../trait/types';
import type { World } from '../world';

export type DeferredRead = Trait | RelationPair;

type HasPeeker = (world: World, entity: Entity, trait: DeferredRead) => boolean | undefined;
type GetPeeker = (
    world: World,
    entity: Entity,
    trait: DeferredRead
) => { value: unknown } | undefined;
type TargetsPeeker = (world: World, entity: Entity, relation: Relation) => Entity[] | undefined;

let peekHas: HasPeeker = () => undefined;
let peekGet: GetPeeker = () => undefined;
let peekTargets: TargetsPeeker = () => undefined;

export function setDeferredPeekers(peekers: {
    has: HasPeeker;
    get: GetPeeker;
    targets: TargetsPeeker;
}): void {
    peekHas = peekers.has;
    peekGet = peekers.get;
    peekTargets = peekers.targets;
}

export function peekDeferredHas(
    world: World,
    entity: Entity,
    trait: DeferredRead
): boolean | undefined {
    return peekHas(world, entity, trait);
}

export function peekDeferredGet(
    world: World,
    entity: Entity,
    trait: DeferredRead
): { value: unknown } | undefined {
    return peekGet(world, entity, trait);
}

export function peekDeferredTargets(
    world: World,
    entity: Entity,
    relation: Relation
): Entity[] | undefined {
    return peekTargets(world, entity, relation);
}

let subscriptionSilence = 0;

export function areSubscriptionsSilenced(): boolean {
    return subscriptionSilence > 0;
}

export function pushSubscriptionSilence(): void {
    subscriptionSilence++;
}

export function popSubscriptionSilence(): void {
    subscriptionSilence--;
}

type MutationGuard = (world: World, entity: Entity) => void;

let beforeMutation: MutationGuard = () => {};

export function setBeforeStructuralMutation(guard: MutationGuard): void {
    beforeMutation = guard;
}

export function beforeStructuralMutation(world: World, entity: Entity): void {
    beforeMutation(world, entity);
}

type ScopeRunner = <T>(world: World, fn: () => T) => T;

let scopeRunner: ScopeRunner = (_world, fn) => fn();

export function setDeferredScopeRunner(runner: ScopeRunner): void {
    scopeRunner = runner;
}

export function runInDeferredScope<T>(world: World, fn: () => T): T {
    return scopeRunner(world, fn);
}
