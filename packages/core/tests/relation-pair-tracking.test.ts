import { beforeEach, describe, expect, it } from 'vitest';
import { $internal, createAdded, createChanged, createRemoved, createWorld, Not, Or, relation, trait } from '../src';

const Position = trait({ x: 0, y: 0 });
const IsActive = trait();

describe('Relation pair tracking', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('tracks a specific target and a wildcard', () => {
        const ChildOf = relation();
        const Added = createAdded();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(ChildOf(parentA));
        const childB = world.spawn(ChildOf(parentB));

        const toA = world.query(Added(ChildOf(parentA)));
        expect(toA).toContain(childA);
        expect(toA).not.toContain(childB);

        const any = world.query(Added(ChildOf('*')));
        expect(any).toContain(childA);
        expect(any).toContain(childB);
    });

    it('detects a pair added when the relation is already present', () => {
        const Likes = relation({ store: { score: 0 } });
        const Added = createAdded();
        const apple = world.spawn();
        const banana = world.spawn();
        const entity = world.spawn(Likes(apple, { score: 1 }));

        expect(world.query(Added(Likes))).toContain(entity);

        entity.add(Likes(banana, { score: 2 }));
        expect(world.query(Added(Likes))).toHaveLength(0);

        const addedBanana = world.query(Added(Likes(banana)));
        expect(addedBanana).toContain(entity);
        addedBanana.readEach(([likes]) => {
            expect(likes.score).toBe(2);
        });
        expect(world.query(Added(Likes('*')))).toContain(entity);
    });

    it('detects a pair removed while other targets remain', () => {
        const Likes = relation();
        const Removed = createRemoved();
        const apple = world.spawn();
        const banana = world.spawn();
        const entity = world.spawn(Likes(apple), Likes(banana));

        entity.remove(Likes(apple));

        expect(world.query(Removed(Likes))).toHaveLength(0);
        expect(world.query(Removed(Likes(apple)))).toContain(entity);
        expect(world.query(Removed(Likes('*')))).toContain(entity);
        expect(entity.has(Likes(banana))).toBe(true);
    });

    it('reports both sides of an exclusive replacement', () => {
        const Parent = relation({ exclusive: true, store: { priority: 0 } });
        const Added = createAdded();
        const Removed = createRemoved();
        const first = world.spawn();
        const second = world.spawn();
        const entity = world.spawn(Parent(first, { priority: 1 }));

        entity.add(Parent(second, { priority: 2 }));

        const removed = world.query(Removed(Parent(first)));
        expect(removed).toContain(entity);
        removed.readEach(([parent]) => {
            expect(parent.priority).toBe(1);
        });

        const added = world.query(Added(Parent(second)));
        expect(added).toContain(entity);
        added.readEach(([parent]) => {
            expect(parent.priority).toBe(2);
        });
        expect(world.query(Added(Parent(first)))).toHaveLength(0);
    });

    it('cancels opposite events on the same target until the query is read', () => {
        const Likes = relation();
        const Added = createAdded();
        const Removed = createRemoved();
        const apple = world.spawn();
        const entity = world.spawn();

        entity.add(Likes(apple));
        entity.remove(Likes(apple));
        expect(world.query(Added(Likes(apple)))).toHaveLength(0);
        expect(world.query(Removed(Likes(apple)))).toHaveLength(0);

        entity.add(Likes(apple));
        expect(world.query(Removed(Likes(apple)))).toHaveLength(0);
        entity.remove(Likes(apple));
        expect(world.query(Removed(Likes(apple)))).toContain(entity);
        expect(world.query(Added(Likes(apple)))).toHaveLength(0);
    });

    it('emits a pair removal for every target when an entity is destroyed', () => {
        const Likes = relation({ store: { score: 0 } });
        const Removed = createRemoved();
        const apple = world.spawn();
        const banana = world.spawn();
        const entity = world.spawn(Likes(apple, { score: 4 }), Likes(banana, { score: 5 }));

        entity.destroy();

        const removedApple = world.query(Removed(Likes(apple)));
        expect(removedApple).toContain(entity);
        removedApple.readEach(([likes]) => {
            expect(likes.score).toBe(4);
        });
        expect(world.query(Removed(Likes(banana)))).toContain(entity);
        expect(world.query(Removed(Likes('*')))).toContain(entity);
    });

    it('composes pair modifiers with Or', () => {
        const ChildOf = relation();
        const Added = createAdded();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(ChildOf(parentA));
        const positioned = world.spawn(Position);

        expect(world.query(Or(Added(ChildOf(parentA)), Added(ChildOf(parentB))))).toContain(childA);
        expect(world.query(Or(Added(ChildOf(parentB)), Added(Position)))).toContain(positioned);
        expect(world.query(Or(Added(ChildOf(parentB)), Added(Position)))).not.toContain(childA);
    });

    it('caches different pair targets as different queries', () => {
        const ChildOf = relation();
        const Added = createAdded();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const start = world[$internal].queriesHashMap.size;

        world.query(Added(ChildOf(parentA)));
        world.query(Added(ChildOf(parentB)));
        expect(world[$internal].queriesHashMap.size).toBe(start + 2);

        world.query(Added(ChildOf(parentA)));
        expect(world[$internal].queriesHashMap.size).toBe(start + 2);
    });

    it('requires pair modifiers and trait parameters together', () => {
        const ChildOf = relation({ store: { priority: 0 } });
        const Added = createAdded();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent, { priority: 3 }));

        expect(world.query(Added(ChildOf(parent)), Position)).toHaveLength(0);
        expect(world.query(Added(ChildOf(parent)), Not(IsActive))).toContain(child);

        child.add(IsActive);
        expect(world.query(Added(ChildOf(parent)), Not(IsActive))).toHaveLength(0);

        child.add(Position({ x: 3, y: 4 }));
        const matched = world.query(Added(ChildOf(parent)), Position);
        expect(matched).toContain(child);
        matched.readEach(([childOf, position]) => {
            expect(childOf.priority).toBe(3);
            expect(position.x).toBe(3);
        });
    });

    it('resolves per-target store data without writing over other targets', () => {
        const Contains = relation({ store: { amount: 0 } });
        const Added = createAdded();
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(
            Contains(gold, { amount: 1 }),
            Contains(silver, { amount: 2 })
        );

        world.query(Added(Contains(gold))).updateEach(([contains]) => {
            expect(contains.amount).toBe(1);
            contains.amount = 8;
        });

        expect(inventory.get(Contains(gold))!.amount).toBe(8);
        expect(inventory.get(Contains(silver))!.amount).toBe(2);

        const any = world.query(Added(Contains('*')));
        any.readEach(([contains]) => {
            expect(typeof contains.amount).toBe('number');
        });
    });

    it('signals pair changes from entity.changed and entity.set', () => {
        const Contains = relation({ store: { amount: 0 } });
        const Changed = createChanged();
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold, { amount: 1 }), Contains(silver, { amount: 2 }));

        inventory.changed(Contains(gold));
        const changedGold = world.query(Changed(Contains(gold)));
        expect(changedGold).toContain(inventory);
        changedGold.readEach(([contains]) => {
            expect(contains.amount).toBe(1);
        });
        expect(world.query(Changed(Contains(silver)))).toHaveLength(0);

        inventory.changed(Contains('*'));
        expect(world.query(Changed(Contains(silver)))).toContain(inventory);

        inventory.set(Contains(silver), { amount: 9 });
        const changedSilver = world.query(Changed(Contains(silver)));
        expect(changedSilver).toContain(inventory);
        changedSilver.readEach(([contains]) => {
            expect(contains.amount).toBe(9);
        });
    });

    it('reuses modifier factories after world reset', () => {
        const ChildOf = relation();
        const Added = createAdded();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent));
        expect(world.query(Added(ChildOf(parent)))).toContain(child);

        world.reset();
        expect(world.query(Added(ChildOf(parent)))).toHaveLength(0);

        const parent2 = world.spawn();
        const child2 = world.spawn(ChildOf(parent2));
        expect(world.query(Added(ChildOf(parent2)))).toContain(child2);
    });

    it('does not include entities spawned after a pair query until the pair is added', () => {
        const ChildOf = relation();
        const Added = createAdded();
        const parent = world.spawn();
        world.query(Added(ChildOf(parent)));

        const child = world.spawn();
        expect(world.query(Added(ChildOf(parent)))).toHaveLength(0);

        child.add(ChildOf(parent));
        expect(world.query(Added(ChildOf(parent)))).toContain(child);
    });

    it('matches a pair only when an added trait in the same modifier was also added', () => {
        const ChildOf = relation({ store: { priority: 0 } });
        const Added = createAdded();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent, { priority: 6 }));

        expect(world.query(Added(Position, ChildOf(parent)))).toHaveLength(0);
        child.add(Position({ x: 1, y: 2 }));

        const matched = world.query(Added(Position, ChildOf(parent)));
        expect(matched).toContain(child);
        matched.readEach(([position, childOf]) => {
            expect(position.x).toBe(1);
            expect(childOf.priority).toBe(6);
        });
    });
});
