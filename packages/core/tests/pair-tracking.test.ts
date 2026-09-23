import { beforeEach, describe, expect, it } from 'vitest';
import {
    $internal,
    createAdded,
    createChanged,
    createRemoved,
    createWorld,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Foo = trait();

// Factories are long-lived and shared across world resets.
const Added = createAdded();
const Removed = createRemoved();
const Changed = createChanged();

describe('Pair tracking modifiers', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('tracks pair additions for a specific target', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();

        world.query(Added(ChildOf(parentA)));

        const childA = world.spawn(ChildOf(parentA));
        world.spawn(ChildOf(parentB));

        expect([...world.query(Added(ChildOf(parentA)))]).toEqual([childA]);
        expect(world.query(Added(ChildOf(parentA)))).toHaveLength(0);
    });

    it('seeds new queries with pair events since the factory was created', () => {
        const ChildOf = relation();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent));

        expect([...world.query(Added(ChildOf(parent)))]).toEqual([child]);
    });

    it('detects non-first pair additions', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const player = world.spawn(Likes(alice));

        const TraitAdded = createAdded();
        const PairAdded = createAdded();
        world.query(TraitAdded(Likes));
        world.query(PairAdded(Likes('*')));
        world.query(PairAdded(Likes(bob)));

        player.add(Likes(bob));

        // Trait-level tracking does not see a second target.
        expect(world.query(TraitAdded(Likes))).toHaveLength(0);
        expect([...world.query(PairAdded(Likes('*')))]).toEqual([player]);
        expect([...world.query(PairAdded(Likes(bob)))]).toEqual([player]);
    });

    it('detects non-last pair removals', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const player = world.spawn(Likes(alice), Likes(bob));

        const TraitRemoved = createRemoved();
        world.query(TraitRemoved(Likes));
        world.query(Removed(Likes(alice)));
        world.query(Removed(Likes(bob)));
        world.query(Removed(Likes('*')));

        player.remove(Likes(alice));

        expect(world.query(TraitRemoved(Likes))).toHaveLength(0);
        expect([...world.query(Removed(Likes(alice)))]).toEqual([player]);
        expect(world.query(Removed(Likes(bob)))).toHaveLength(0);
        expect([...world.query(Removed(Likes('*')))]).toEqual([player]);
    });

    it('reports exclusive replacement as a removal and an addition', () => {
        const Targeting = relation({ exclusive: true });
        const a = world.spawn();
        const b = world.spawn();
        const unit = world.spawn(Targeting(a));

        world.query(Removed(Targeting(a)));
        world.query(Added(Targeting(b)));
        world.query(Added(Targeting(a)));

        unit.add(Targeting(b));

        expect([...world.query(Removed(Targeting(a)))]).toEqual([unit]);
        expect([...world.query(Added(Targeting(b)))]).toEqual([unit]);
        expect(world.query(Added(Targeting(a)))).toHaveLength(0);
    });

    it('keeps working when factories are reused across world resets', () => {
        const ChildOf = relation();
        const parent = world.spawn();
        world.spawn(ChildOf(parent));
        world.query(Added(ChildOf(parent)));

        world.reset();

        const newParent = world.spawn();
        const newChild = world.spawn(ChildOf(newParent));
        expect([...world.query(Added(ChildOf(newParent)))]).toEqual([newChild]);
        expect(world.query(Added(Foo))).toHaveLength(0);
    });

    it('cancels opposite pair events on the same target within a window', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const player = world.spawn(Likes(bob));

        world.query(Added(Likes(alice)));
        world.query(Removed(Likes(alice)));
        world.query(Added(Likes(bob)));
        world.query(Removed(Likes(bob)));

        // Add then remove
        player.add(Likes(alice));
        player.remove(Likes(alice));
        // Remove then add
        player.remove(Likes(bob));
        player.add(Likes(bob));

        expect(world.query(Added(Likes(alice)))).toHaveLength(0);
        expect(world.query(Removed(Likes(alice)))).toHaveLength(0);
        expect(world.query(Added(Likes(bob)))).toHaveLength(0);
        expect(world.query(Removed(Likes(bob)))).toHaveLength(0);

        // Events in separate windows do not cancel.
        player.add(Likes(alice));
        expect([...world.query(Added(Likes(alice)))]).toEqual([player]);
        expect(world.query(Removed(Likes(alice)))).toHaveLength(0);
        player.remove(Likes(alice));
        expect([...world.query(Removed(Likes(alice)))]).toEqual([player]);
    });

    it('fires pair-level removal for all pairs on entity destruction', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const player = world.spawn(Likes(alice), Likes(bob));
        const fan = world.spawn(Likes(player));

        world.query(Removed(Likes(alice)));
        world.query(Removed(Likes(bob)));
        world.query(Removed(Likes(player)));

        player.destroy();

        expect([...world.query(Removed(Likes(alice)))]).toEqual([player]);
        expect([...world.query(Removed(Likes(bob)))]).toEqual([player]);
        // Sources pointing at the destroyed entity lose their pair too.
        expect([...world.query(Removed(Likes(player)))]).toEqual([fan]);
    });

    it('tracks pair changes via set and entity.changed', () => {
        const Contains = relation({ store: { amount: 0 } });
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold), Contains(silver));

        world.query(Changed(Contains(gold)));
        world.query(Changed(Contains(silver)));

        inventory.set(Contains(gold), { amount: 5 });
        expect([...world.query(Changed(Contains(gold)))]).toEqual([inventory]);
        expect(world.query(Changed(Contains(silver)))).toHaveLength(0);

        inventory.changed(Contains(silver));
        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
        expect([...world.query(Changed(Contains(silver)))]).toEqual([inventory]);

        inventory.changed(Contains('*'));
        expect([...world.query(Changed(Contains(gold)))]).toEqual([inventory]);
        expect([...world.query(Changed(Contains(silver)))]).toEqual([inventory]);
    });

    it('drops a pending pair change when the pair is removed', () => {
        const Contains = relation({ store: { amount: 0 } });
        const gold = world.spawn();
        const inventory = world.spawn(Contains(gold));

        world.query(Changed(Contains(gold)));
        inventory.set(Contains(gold), { amount: 1 });
        inventory.remove(Contains(gold));

        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
    });

    it('composes pair modifiers with Or', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();

        const query = () => world.query(Or(Added(Likes(alice)), Added(Likes(bob)), Added(Foo)));
        query();

        a.add(Likes(alice));
        b.add(Likes(bob));
        c.add(Foo);

        const result = query();
        expect(result).toHaveLength(3);
        expect(result).toContain(a);
        expect(result).toContain(b);
        expect(result).toContain(c);
        expect(query()).toHaveLength(0);
    });

    it('requires all pairs in a single modifier', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const player = world.spawn();

        world.query(Added(Likes(alice), Likes(bob)));

        player.add(Likes(alice));
        expect(world.query(Added(Likes(alice), Likes(bob)))).toHaveLength(0);

        player.add(Likes(bob));
        expect([...world.query(Added(Likes(alice), Likes(bob)))]).toEqual([player]);
    });

    it('creates distinct cached queries for different pair targets', () => {
        const Likes = relation();
        const alice = world.spawn();
        const bob = world.spawn();
        const ctx = world[$internal];

        world.query(Added(Likes(alice)));
        world.query(Added(Likes(bob)));
        world.query(Added(Likes('*')));
        world.query(Added(Likes));
        world.query(Or(Added(Likes(alice))));
        world.query(Or(Added(Likes(bob))));

        const hashes = new Set(ctx.queriesHashMap.keys());
        expect(hashes.size).toBe(ctx.queriesHashMap.size);
        expect(ctx.queriesHashMap.size).toBeGreaterThanOrEqual(6);

        const before = ctx.queriesHashMap.size;
        world.query(Added(Likes(alice)));
        expect(ctx.queriesHashMap.size).toBe(before);
    });

    it('satisfies regular trait parameters together with pair modifiers', () => {
        const Likes = relation();
        const alice = world.spawn();
        const withPosition = world.spawn(Position);
        const withoutPosition = world.spawn();
        const excluded = world.spawn(Position, Foo);

        const query = () => world.query(Added(Likes(alice)), Position, Not(Foo));
        query();

        withPosition.add(Likes(alice));
        withoutPosition.add(Likes(alice));
        excluded.add(Likes(alice));

        expect([...query()]).toEqual([withPosition]);

        const Moved = createChanged();
        const both = () => world.query(Moved(Position), Changed(Likes(alice)));
        both();

        withPosition.changed(Position);
        expect(both()).toHaveLength(0);

        withPosition.changed(Position);
        withPosition.changed(Likes(alice));
        expect([...both()]).toEqual([withPosition]);
    });

    it('resolves per-target relation data when iterating pair-tracked results', () => {
        const Contains = relation({ store: { amount: 0 } });
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(
            Contains(gold, { amount: 10 }),
            Contains(silver, { amount: 20 })
        );

        world.query(Changed(Contains(silver)));
        inventory.changed(Contains(silver));

        world.query(Changed(Contains(silver))).readEach(([contains], entity) => {
            expect(entity).toBe(inventory);
            expect(contains).toEqual({ amount: 20 });
        });

        const OtherChanged = createChanged();
        world.query(OtherChanged(Contains(gold)));

        let visited = 0;
        world.query(Added(Contains(gold))).updateEach(
            ([contains]) => {
                visited++;
                expect(contains.amount).toBe(10);
                contains.amount = 15;
            },
            { changeDetection: 'always' }
        );
        expect(visited).toBe(1);

        expect(inventory.get(Contains(gold))).toEqual({ amount: 15 });
        expect(inventory.get(Contains(silver))).toEqual({ amount: 20 });
        expect([...world.query(OtherChanged(Contains(gold)))]).toEqual([inventory]);
    });

    it('notifies query subscriptions for pair events', () => {
        const Likes = relation();
        const alice = world.spawn();
        const player = world.spawn();
        const added: number[] = [];

        world.onQueryAdd([Added(Likes(alice))], (entity) => added.push(entity));
        player.add(Likes(alice));

        expect(added).toEqual([player]);
    });
});
