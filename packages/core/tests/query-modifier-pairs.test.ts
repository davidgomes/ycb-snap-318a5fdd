import { beforeEach, describe, expect, it } from 'vitest';
import {
    $internal,
    createAdded,
    createChanged,
    createRemoved,
    createWorld,
    type Entity,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const list = (result: readonly Entity[]) => [...result];

const Position = trait({ x: 0, y: 0 });
const Foo = trait();

// Created once and reused across world resets.
const LongLivedAdded = createAdded();
const LongLivedRemoved = createRemoved();
const LongLivedChanged = createChanged();

describe('Tracking modifiers with relation pairs', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    describe('Added', () => {
        it('tracks a specific target, including non-first pair additions', () => {
            const Added = createAdded();
            const Likes = relation();

            const alice = world.spawn();
            const bob = world.spawn();
            const entity = world.spawn();

            expect(list(world.query(Added(Likes(bob))))).toHaveLength(0);

            entity.add(Likes(alice));
            expect(list(world.query(Added(Likes(bob))))).toHaveLength(0);

            // Non-first addition does not change the trait bitmask but is detected per pair.
            entity.add(Likes(bob));
            expect(list(world.query(Added(Likes(bob))))).toEqual([entity]);
            expect(list(world.query(Added(Likes(bob))))).toHaveLength(0);
        });

        it('tracks any target with a wildcard', () => {
            const Added = createAdded();
            const AddedTrait = createAdded();
            const Likes = relation();

            const alice = world.spawn();
            const bob = world.spawn();
            const entity = world.spawn();

            world.query(Added(Likes('*')));
            world.query(AddedTrait(Likes));

            entity.add(Likes(alice));
            expect(list(world.query(Added(Likes('*'))))).toEqual([entity]);
            expect(list(world.query(AddedTrait(Likes)))).toEqual([entity]);

            entity.add(Likes(bob));
            expect(list(world.query(Added(Likes('*'))))).toEqual([entity]);
            // Trait-level tracking only sees the first pair.
            expect(list(world.query(AddedTrait(Likes)))).toHaveLength(0);
        });

        it('populates queries registered after the pair was added', () => {
            const Added = createAdded();
            const ChildOf = relation();

            const parentA = world.spawn();
            const parentB = world.spawn();
            const childA = world.spawn(ChildOf(parentA));
            const childB = world.spawn(ChildOf(parentB));

            expect(list(world.query(Added(ChildOf(parentA))))).toEqual([childA]);
            expect(list(world.query(Added(ChildOf(parentB))))).toEqual([childB]);
            expect(list(world.query(Added(ChildOf(parentA))))).toHaveLength(0);
        });
    });

    describe('Removed', () => {
        it('tracks a specific target, including non-last pair removals', () => {
            const Removed = createRemoved();
            const Likes = relation();

            const alice = world.spawn();
            const bob = world.spawn();
            const entity = world.spawn(Likes(alice), Likes(bob));

            expect(list(world.query(Removed(Likes(alice))))).toHaveLength(0);

            // The entity still has Likes(bob) so the trait itself is not removed.
            entity.remove(Likes(alice));
            expect(entity.has(Likes('*'))).toBe(true);
            expect(list(world.query(Removed(Likes(alice))))).toEqual([entity]);
            expect(list(world.query(Removed(Likes(bob))))).toHaveLength(0);
            expect(list(world.query(Removed(Likes(alice))))).toHaveLength(0);

            entity.remove(Likes(bob));
            expect(list(world.query(Removed(Likes(bob))))).toEqual([entity]);
        });

        it('tracks any target with a wildcard', () => {
            const Removed = createRemoved();
            const RemovedTrait = createRemoved();
            const Likes = relation();

            const alice = world.spawn();
            const bob = world.spawn();
            const entity = world.spawn(Likes(alice), Likes(bob));

            world.query(Removed(Likes('*')));
            world.query(RemovedTrait(Likes));

            entity.remove(Likes(alice));
            expect(list(world.query(Removed(Likes('*'))))).toEqual([entity]);
            expect(list(world.query(RemovedTrait(Likes)))).toHaveLength(0);

            entity.remove(Likes(bob));
            expect(list(world.query(Removed(Likes('*'))))).toEqual([entity]);
            expect(list(world.query(RemovedTrait(Likes)))).toEqual([entity]);
        });

        it('populates queries registered after the pair was removed', () => {
            const Removed = createRemoved();
            const Likes = relation();

            const alice = world.spawn();
            const bob = world.spawn();
            const entity = world.spawn(Likes(alice), Likes(bob));
            entity.remove(Likes(alice));

            expect(list(world.query(Removed(Likes(alice))))).toEqual([entity]);
            expect(list(world.query(Removed(Likes(bob))))).toHaveLength(0);
        });
    });

    it('produces a removal and an addition on exclusive replacement', () => {
        const Added = createAdded();
        const Removed = createRemoved();
        const Targeting = relation({ exclusive: true });

        const a = world.spawn();
        const b = world.spawn();
        const entity = world.spawn(Targeting(a));

        world.query(Added(Targeting(b)));
        world.query(Removed(Targeting(a)));
        world.query(Added(Targeting('*')));
        world.query(Removed(Targeting('*')));

        entity.add(Targeting(b));

        expect(list(world.query(Added(Targeting(b))))).toEqual([entity]);
        expect(list(world.query(Removed(Targeting(a))))).toEqual([entity]);
        expect(list(world.query(Added(Targeting('*'))))).toEqual([entity]);
        expect(list(world.query(Removed(Targeting('*'))))).toEqual([entity]);
    });

    it('cancels opposite pair events on the same target within an observation window', () => {
        const Added = createAdded();
        const Removed = createRemoved();
        const Likes = relation();

        const alice = world.spawn();
        const bob = world.spawn();
        const entity = world.spawn(Likes(bob));

        world.query(Added(Likes(alice)));
        world.query(Removed(Likes(alice)));
        world.query(Added(Likes(bob)));
        world.query(Removed(Likes(bob)));
        world.query(Added(Likes('*')));
        world.query(Removed(Likes('*')));

        // A removal cancels the pending addition.
        entity.add(Likes(alice));
        entity.remove(Likes(alice));
        expect(list(world.query(Added(Likes(alice))))).toHaveLength(0);
        expect(list(world.query(Added(Likes('*'))))).toHaveLength(0);
        expect(list(world.query(Removed(Likes(alice))))).toEqual([entity]);
        expect(list(world.query(Removed(Likes('*'))))).toEqual([entity]);

        // An addition cancels the pending removal.
        entity.remove(Likes(bob));
        entity.add(Likes(bob));
        expect(list(world.query(Removed(Likes(bob))))).toHaveLength(0);
        expect(list(world.query(Removed(Likes('*'))))).toHaveLength(0);
        expect(list(world.query(Added(Likes(bob))))).toEqual([entity]);
        expect(list(world.query(Added(Likes('*'))))).toEqual([entity]);

        // Events on different targets do not cancel.
        entity.remove(Likes(bob));
        entity.add(Likes(alice));
        expect(list(world.query(Removed(Likes(bob))))).toEqual([entity]);
        expect(list(world.query(Added(Likes(alice))))).toEqual([entity]);
        expect(list(world.query(Removed(Likes('*'))))).toEqual([entity]);
        expect(list(world.query(Added(Likes('*'))))).toEqual([entity]);
    });

    describe('Changed', () => {
        it('tracks changes to a specific target', () => {
            const Changed = createChanged();
            const Contains = relation({ store: { amount: 0 } });

            const gold = world.spawn();
            const silver = world.spawn();
            const inventory = world.spawn(Contains(gold), Contains(silver));

            expect(list(world.query(Changed(Contains(gold))))).toHaveLength(0);
            expect(list(world.query(Changed(Contains(silver))))).toHaveLength(0);

            inventory.set(Contains(silver), { amount: 5 });
            expect(list(world.query(Changed(Contains(gold))))).toHaveLength(0);
            expect(list(world.query(Changed(Contains(silver))))).toEqual([inventory]);
            expect(list(world.query(Changed(Contains(silver))))).toHaveLength(0);

            inventory.set(Contains(gold), { amount: 1 });
            expect(list(world.query(Changed(Contains('*'))))).toEqual([inventory]);
        });

        it('populates queries registered after the pair changed', () => {
            const Changed = createChanged();
            const Contains = relation({ store: { amount: 0 } });

            const gold = world.spawn();
            const silver = world.spawn();
            const inventory = world.spawn(Contains(gold), Contains(silver));
            inventory.set(Contains(gold), { amount: 3 });

            expect(list(world.query(Changed(Contains(gold))))).toEqual([inventory]);
            expect(list(world.query(Changed(Contains(silver))))).toHaveLength(0);
        });

        it('accepts relation pairs in entity.changed', () => {
            const Changed = createChanged();
            const Contains = relation({ store: { amount: 0 } });

            const gold = world.spawn();
            const silver = world.spawn();
            const inventory = world.spawn(Contains(gold), Contains(silver));

            world.query(Changed(Contains(gold)));
            world.query(Changed(Contains(silver)));

            inventory.changed(Contains(gold));
            expect(list(world.query(Changed(Contains(gold))))).toEqual([inventory]);
            expect(list(world.query(Changed(Contains(silver))))).toHaveLength(0);

            inventory.changed(Contains('*'));
            expect(list(world.query(Changed(Contains(gold))))).toEqual([inventory]);
            expect(list(world.query(Changed(Contains(silver))))).toEqual([inventory]);

            // Flagging a pair the entity does not have is a no-op.
            const copper = world.spawn();
            inventory.changed(Contains(copper));
            expect(list(world.query(Changed(Contains(copper))))).toHaveLength(0);
        });

        it('forgets a change when the pair is removed', () => {
            const Changed = createChanged();
            const Contains = relation({ store: { amount: 0 } });

            const gold = world.spawn();
            const silver = world.spawn();
            const inventory = world.spawn(Contains(gold), Contains(silver));

            world.query(Changed(Contains(gold)));

            inventory.set(Contains(gold), { amount: 2 });
            inventory.remove(Contains(gold));
            expect(list(world.query(Changed(Contains(gold))))).toHaveLength(0);

            inventory.add(Contains(gold));
            expect(list(world.query(Changed(Contains(gold))))).toHaveLength(0);
        });
    });

    it('fires pair-level removals for all active pairs when an entity is destroyed', () => {
        const Removed = createRemoved();
        const Likes = relation();

        const alice = world.spawn();
        const bob = world.spawn();
        const source = world.spawn(Likes(alice), Likes(bob));
        const fan = world.spawn(Likes(bob));

        world.query(Removed(Likes(alice)));
        world.query(Removed(Likes(bob)));
        world.query(Removed(Likes('*')));

        source.destroy();

        expect(list(world.query(Removed(Likes(alice))))).toEqual([source]);
        expect(list(world.query(Removed(Likes(bob))))).toEqual([source]);
        expect(list(world.query(Removed(Likes('*'))))).toEqual([source]);

        // Destroying a target removes the pairs pointing to it.
        bob.destroy();
        expect(list(world.query(Removed(Likes(bob))))).toEqual([fan]);
    });

    it('composes pair modifiers with Or', () => {
        const Added = createAdded();
        const Likes = relation();

        const alice = world.spawn();
        const bob = world.spawn();
        const carol = world.spawn();
        const entityA = world.spawn();
        const entityB = world.spawn();
        const entityC = world.spawn();

        const query = () => world.query(Or(Added(Likes(alice)), Added(Likes(bob)), Added(Foo)));
        expect(query()).toHaveLength(0);

        entityA.add(Likes(alice));
        entityB.add(Likes(carol), Likes(bob));
        entityC.add(Likes(carol));
        expect(list(query()).sort()).toEqual([entityA, entityB].sort());

        entityC.add(Foo);
        expect(list(query())).toEqual([entityC]);
    });

    it('creates distinct cached queries for different pair targets', () => {
        const ctx = world[$internal];
        const Added = createAdded();
        const Likes = relation();

        const alice = world.spawn();
        const bob = world.spawn();
        const entity = world.spawn(Likes(alice));

        const sizeBefore = ctx.queriesHashMap.size;
        const forAlice = world.query(Added(Likes(alice)));
        const forBob = world.query(Added(Likes(bob)));
        const forAny = world.query(Added(Likes('*')));
        const inOr = world.query(Or(Added(Likes(bob))));

        expect(ctx.queriesHashMap.size).toBe(sizeBefore + 4);
        expect(list(forAlice)).toEqual([entity]);
        expect(forBob).toHaveLength(0);
        expect(list(forAny)).toEqual([entity]);
        expect(inOr).toHaveLength(0);

        // The same pair hashes to the same query.
        world.query(Added(Likes(alice)));
        expect(ctx.queriesHashMap.size).toBe(sizeBefore + 4);
    });

    it('requires all constraints when combined with regular trait parameters', () => {
        const Added = createAdded();
        const Likes = relation();

        const alice = world.spawn();
        const withPosition = world.spawn(Position, Likes(alice));
        const withoutPosition = world.spawn(Likes(alice));
        const excluded = world.spawn(Position, Foo, Likes(alice));

        // Initial population.
        expect(list(world.query(Added(Likes(alice)), Position, Not(Foo)))).toEqual([withPosition]);

        // Incremental updates.
        const bob = world.spawn();
        withPosition.add(Likes(bob));
        withoutPosition.add(Likes(bob));
        excluded.add(Likes(bob));
        expect(list(world.query(Added(Likes(bob)), Position, Not(Foo)))).toEqual([withPosition]);

        const dave = world.spawn();
        withoutPosition.add(Likes(dave));
        expect(list(world.query(Added(Likes(dave)), Position))).toHaveLength(0);
        withoutPosition.add(Position);
        expect(list(world.query(Added(Likes(dave)), Position))).toEqual([withoutPosition]);
    });

    it('keeps working with modifier factories reused across world resets', () => {
        const Likes = relation({ store: { weight: 0 } });

        for (let round = 0; round < 2; round++) {
            world.reset();

            const alice = world.spawn();
            const bob = world.spawn();
            const entity = world.spawn(Likes(alice));

            expect(list(world.query(LongLivedAdded(Likes(alice))))).toEqual([entity]);
            expect(list(world.query(LongLivedAdded(Position)))).toHaveLength(0);

            entity.add(Likes(bob));
            entity.set(Likes(alice), { weight: 1 });
            entity.remove(Likes(alice));

            expect(list(world.query(LongLivedAdded(Likes(bob))))).toEqual([entity]);
            expect(list(world.query(LongLivedRemoved(Likes(alice))))).toEqual([entity]);
            expect(list(world.query(LongLivedChanged(Likes(alice))))).toHaveLength(0);

            entity.set(Likes(bob), { weight: 2 });
            expect(list(world.query(LongLivedChanged(Likes(bob))))).toEqual([entity]);
        }
    });

    it('resolves per-target relation data when iterating results', () => {
        const Changed = createChanged();
        const Observe = createChanged();
        const Contains = relation({ store: { amount: 0 } });

        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold, { amount: 1 }), Contains(silver, { amount: 7 }));

        world.query(Observe(Contains(gold)));
        world.query(Observe(Contains(silver)));

        inventory.changed(Contains(silver));

        const read: number[] = [];
        world.query(Changed(Contains(silver))).readEach(([contains]) => {
            read.push(contains.amount);
        });
        expect(read).toEqual([7]);

        inventory.changed(Contains(silver));
        world.query(Changed(Contains(silver))).updateEach(([contains]) => {
            contains.amount = 10;
        });

        expect(inventory.get(Contains(silver))!.amount).toBe(10);
        expect(inventory.get(Contains(gold))!.amount).toBe(1);

        // Writes through a pair-tracked trait signal a change for that pair only.
        expect(list(world.query(Observe(Contains(silver))))).toEqual([inventory]);
        expect(list(world.query(Observe(Contains(gold))))).toHaveLength(0);
    });

    it('resolves per-target data for AoS relations', () => {
        const Added = createAdded();
        const Holds = relation({ store: () => ({ label: '' }) });

        const a = world.spawn();
        const b = world.spawn();
        const entity = world.spawn(Holds(a, { label: 'a' }), Holds(b, { label: 'b' }));

        const labels: string[] = [];
        world.query(Added(Holds(b))).readEach(([holds]) => {
            labels.push(holds.label);
        });
        expect(labels).toEqual(['b']);
        expect(entity.get(Holds(a))!.label).toBe('a');
    });
});
