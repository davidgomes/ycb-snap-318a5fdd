import { beforeEach, describe, expect, it } from 'vitest';
import { createAdded, createChanged, createRemoved, createWorld, Not, Or, relation, trait } from '../src';

describe('Relation pair tracking', () => {
    const world = createWorld();
    const Added = createAdded();
    const Removed = createRemoved();
    const Changed = createChanged();

    beforeEach(() => {
        world.reset();
    });

    it('detects non-first pair additions and ignores trait-level Added', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn();

        child.add(ChildOf(parentA));
        expect(world.query(Added(ChildOf))).toContain(child);

        child.add(ChildOf(parentB));
        expect(world.query(Added(ChildOf))).toHaveLength(0);
        expect(world.query(Added(ChildOf(parentB)))).toContain(child);
        expect(world.query(Added(ChildOf('*')))).toContain(child);
        expect(world.query(Added(ChildOf(parentA)))).toContain(child);
    });

    it('detects non-last pair removals', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA), ChildOf(parentB));
        world.query(Removed(ChildOf(parentA)));
        world.query(Removed(ChildOf('*')));
        world.query(Removed(ChildOf));

        child.remove(ChildOf(parentA));

        expect(world.query(Removed(ChildOf(parentA)))).toContain(child);
        expect(world.query(Removed(ChildOf('*')))).toContain(child);
        expect(world.query(Removed(ChildOf))).toHaveLength(0);
        expect(child.has(ChildOf(parentB))).toBe(true);
    });

    it('emits both a removal and an addition when an exclusive target is replaced', () => {
        const Targeting = relation({ exclusive: true });
        const enemy = world.spawn();
        const playerA = world.spawn();
        const playerB = world.spawn();

        enemy.add(Targeting(playerA));
        world.query(Added(Targeting(playerA)));
        world.query(Removed(Targeting(playerA)));
        world.query(Added(Targeting(playerB)));
        world.query(Removed(Targeting('*')));
        world.query(Added(Targeting('*')));

        enemy.add(Targeting(playerB));

        expect(world.query(Removed(Targeting(playerA)))).toContain(enemy);
        expect(world.query(Added(Targeting(playerB)))).toContain(enemy);
        expect(world.query(Removed(Targeting('*')))).toContain(enemy);
        expect(world.query(Added(Targeting('*')))).toContain(enemy);
        expect(enemy.has(Targeting(playerA))).toBe(false);
        expect(enemy.has(Targeting(playerB))).toBe(true);
    });

    it('cancels opposite pair events on the same target inside one observation window', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn();

        world.query(Added(ChildOf(parentA)));
        world.query(Removed(ChildOf(parentA)));
        world.query(Added(ChildOf(parentB)));

        child.add(ChildOf(parentA));
        child.add(ChildOf(parentB));
        child.remove(ChildOf(parentA));

        expect(world.query(Added(ChildOf(parentA)))).toHaveLength(0);
        expect(world.query(Removed(ChildOf(parentA)))).toHaveLength(0);
        expect(world.query(Added(ChildOf(parentB)))).toContain(child);
        expect(world.query(Added(ChildOf('*')))).toContain(child);
    });

    it('starts a new observation window when the pair query is read', () => {
        const ChildOf = relation();
        const parent = world.spawn();
        const child = world.spawn();

        world.query(Removed(ChildOf(parent)));
        child.add(ChildOf(parent));
        expect(world.query(Removed(ChildOf(parent)))).toHaveLength(0);

        child.remove(ChildOf(parent));
        expect(world.query(Removed(ChildOf(parent)))).toContain(child);
    });

    it('reuses tracking factories after the world is reset', () => {
        const ChildOf = relation();
        const parent = world.spawn();
        const child = world.spawn();
        child.add(ChildOf(parent));
        expect(world.query(Added(ChildOf(parent)))).toContain(child);

        world.reset();

        const parent2 = world.spawn();
        const child2 = world.spawn();
        child2.add(ChildOf(parent2));
        expect(world.query(Added(ChildOf(parent2)))).toContain(child2);
        expect(world.query(Added(ChildOf(parent)))).toHaveLength(0);
    });

    it('fires a pair removal for every active pair when an entity is destroyed', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA), ChildOf(parentB));
        world.query(Removed(ChildOf(parentA)));
        world.query(Removed(ChildOf(parentB)));
        world.query(Removed(ChildOf('*')));

        child.destroy();

        expect(world.query(Removed(ChildOf(parentA)))).toContain(child);
        expect(world.query(Removed(ChildOf(parentB)))).toContain(child);
        expect(world.query(Removed(ChildOf('*')))).toContain(child);
    });

    it('fires a pair removal on the source when the target entity is destroyed', () => {
        const ChildOf = relation();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent));
        world.query(Removed(ChildOf(parent)));

        parent.destroy();

        expect(world.query(Removed(ChildOf(parent)))).toContain(child);
    });

    it('composes pair modifiers with Or', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn();
        const childB = world.spawn();

        childA.add(ChildOf(parentA));
        childB.add(ChildOf(parentB));

        const either = world.query(Or(Added(ChildOf(parentA)), Added(ChildOf(parentB))));
        expect(either).toContain(childA);
        expect(either).toContain(childB);
        expect(either).toHaveLength(2);
    });

    it('caches distinct queries for different pair targets', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn();

        child.add(ChildOf(parentA));

        const toA = world.query(Added(ChildOf(parentA)));
        const toB = world.query(Added(ChildOf(parentB)));
        expect(toA).toContain(child);
        expect(toB).not.toContain(child);

        child.add(ChildOf(parentB));
        expect(world.query(Added(ChildOf(parentB)))).toContain(child);
        expect(world.query(Added(ChildOf(parentA)))).toHaveLength(0);
    });

    it('requires pair modifiers and regular trait parameters together', () => {
        const ChildOf = relation();
        const IsEnemy = trait();
        const parent = world.spawn();
        const friend = world.spawn();
        const enemy = world.spawn(IsEnemy);

        friend.add(ChildOf(parent));
        enemy.add(ChildOf(parent));

        const threats = world.query(Added(ChildOf(parent)), IsEnemy);
        expect(threats).toContain(enemy);
        expect(threats).not.toContain(friend);

        const alive = world.spawn();
        const dead = world.spawn(IsEnemy);
        alive.add(ChildOf(parent));
        dead.add(ChildOf(parent));

        const livingChildren = world.query(Added(ChildOf(parent)), Not(IsEnemy));
        expect(livingChildren).toContain(alive);
        expect(livingChildren).not.toContain(dead);
        expect(livingChildren).not.toContain(enemy);
    });

    it('tracks manual pair changes from entity.changed and resolves per-target data', () => {
        const Contains = relation({ store: { amount: 0 } });
        const inventory = world.spawn();
        const gold = world.spawn();
        const silver = world.spawn();

        inventory.add(Contains(gold, { amount: 1 }), Contains(silver, { amount: 2 }));
        world.query(Changed(Contains(gold)));

        inventory.changed(Contains(gold));

        const changed = world.query(Changed(Contains(gold)));
        expect(changed).toContain(inventory);
        changed.readEach(([data]) => {
            expect(data).toEqual({ amount: 1 });
        });
        expect(world.query(Changed(Contains(silver)))).toHaveLength(0);

        inventory.changed(Contains('*'));
        expect(world.query(Changed(Contains(gold)))).toContain(inventory);
        expect(world.query(Changed(Contains(silver)))).toContain(inventory);
    });

    it('writes per-target relation data from updateEach', () => {
        const Contains = relation({ store: { amount: 0 } });
        const inventory = world.spawn();
        const gold = world.spawn();
        const silver = world.spawn();

        inventory.add(Contains(gold, { amount: 1 }), Contains(silver, { amount: 2 }));
        inventory.changed(Contains(gold));

        world.query(Changed(Contains(gold))).updateEach(([data]) => {
            expect(data.amount).toBe(1);
            data.amount = 4;
        });

        expect(inventory.get(Contains(gold))!.amount).toBe(4);
        expect(inventory.get(Contains(silver))!.amount).toBe(2);
    });

    it('requires every pair when several pairs are passed to one modifier', () => {
        const ChildOf = relation();
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn();

        child.add(ChildOf(parentA));
        expect(world.query(Added(ChildOf(parentA), ChildOf(parentB)))).toHaveLength(0);

        child.add(ChildOf(parentB));
        expect(world.query(Added(ChildOf(parentA), ChildOf(parentB)))).toContain(child);
    });
});
