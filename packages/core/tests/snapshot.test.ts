import { beforeEach, describe, expect, it } from 'vitest';
import {
    createTraitRegistry,
    createWorld,
    diffEntitySnapshots,
    diffWorldSnapshots,
    relation,
    rollbackEntity,
    rollbackWorld,
    snapshotEntity,
    snapshotWorld,
    trait,
    type EntitySnapshot,
    type WorldSnapshot,
} from '../src';

class Vec {
    constructor(
        public x = 0,
        public y = 0
    ) {}

    mag() {
        return Math.hypot(this.x, this.y);
    }
}

const Position = trait({ x: 0, y: 0 });
const Health = trait({ value: 100, meta: () => ({ regen: 1 }) });
const Dead = trait();
const Velocity = trait(() => new Vec());
const ChildOf = relation({ exclusive: true });
const Contains = relation({ store: { amount: 0, meta: () => ({ quality: 1 }) } });
const Likes = relation();

describe('Snapshot', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    function registry() {
        return createTraitRegistry(
            ['position', Position],
            ['health', Health],
            ['dead', Dead],
            ['velocity', Velocity],
            ['childOf', ChildOf],
            ['contains', Contains],
            ['likes', Likes]
        );
    }

    it('throws when a registry key, trait, or relation is duplicated', () => {
        expect(() => createTraitRegistry(['a', Position], ['a', Health])).toThrow(Error);
        expect(() => createTraitRegistry(['a', Position], ['b', Position])).toThrow(Error);
        expect(() => createTraitRegistry(['a', ChildOf], ['b', ChildOf])).toThrow(Error);
        expect(() => createTraitRegistry(['position', Position], ['childOf', ChildOf])).not.toThrow();
    });

    it('snapshots tag traits as true and data traits as deep copies', () => {
        const names = registry();
        const entity = world.spawn(Dead, Position({ x: 2, y: 4 }), Health({ value: 7 }));
        const snapshot = snapshotEntity(world, entity, names);

        expect(snapshot.id).toBe(entity.id());
        expect(snapshot.traits.dead).toBe(true);
        expect(snapshot.traits.position).toEqual({ x: 2, y: 4 });
        expect(snapshot.traits.position).not.toBe(entity.get(Position));
        expect(snapshot.traits.health).toEqual({ value: 7, meta: { regen: 1 } });
        expect(snapshot.traits.health).not.toHaveProperty('relations');
        expect(snapshot).not.toHaveProperty('relations');

        const health = snapshot.traits.health as { value: number; meta: { regen: number } };
        health.value = 1;
        health.meta.regen = 9;
        expect(entity.get(Health)).toEqual({ value: 7, meta: { regen: 1 } });

        entity.set(Position, { x: 8, y: 9 });
        entity.set(Health, { meta: { regen: 3 } });
        expect(snapshot.traits.position).toEqual({ x: 2, y: 4 });
        expect(health.meta.regen).toBe(9);
        expect((snapshot.traits.health as { meta: { regen: number } }).meta.regen).toBe(9);
    });

    it('deep copies AoS trait instances without sharing nested state', () => {
        const names = registry();
        const entity = world.spawn(Velocity);
        entity.get(Velocity)!.x = 3;
        entity.get(Velocity)!.y = 4;

        const snapshot = snapshotEntity(world, entity, names);
        const copy = snapshot.traits.velocity as Vec;

        expect(copy).not.toBe(entity.get(Velocity));
        expect(copy).toBeInstanceOf(Vec);
        expect(copy.mag()).toBe(5);

        copy.x = 10;
        expect(entity.get(Velocity)!.x).toBe(3);
        entity.get(Velocity)!.y = 1;
        expect(copy.y).toBe(4);
    });

    it('omits relation data unless the relation has a store', () => {
        const names = registry();
        const parent = world.spawn();
        const item = world.spawn();
        const other = world.spawn();
        const entity = world.spawn(ChildOf(parent), Contains(item, { amount: 5 }), Likes(other));
        entity.add(Likes(parent));

        const snapshot = snapshotEntity(world, entity, names);
        const childOf = snapshot.relations?.childOf;
        const contains = snapshot.relations?.contains;
        const likes = snapshot.relations?.likes;

        expect(snapshot.traits).toEqual({});
        expect(childOf).toEqual([{ targetId: parent.id() }]);
        expect(childOf?.[0]).not.toHaveProperty('data');
        expect(likes).toEqual([{ targetId: other.id() }, { targetId: parent.id() }]);
        expect(likes?.[0]).not.toHaveProperty('data');
        expect(contains).toEqual([
            { targetId: item.id(), data: { amount: 5, meta: { quality: 1 } } },
        ]);

        const data = contains?.[0].data as { amount: number; meta: { quality: number } };
        data.amount = 0;
        data.meta.quality = 4;
        expect(entity.get(Contains(item))).toEqual({ amount: 5, meta: { quality: 1 } });

        entity.set(Contains(item), { amount: 2 });
        expect(data.amount).toBe(0);
    });

    it('throws when snapshotting a destroyed entity or an unregistered trait or relation', () => {
        const names = registry();
        const Secret = trait();
        const SecretRel = relation();
        const entity = world.spawn(Secret);
        const target = world.spawn();

        expect(() => snapshotEntity(world, entity, names)).toThrow(Error);
        entity.remove(Secret);
        entity.add(SecretRel(target));
        expect(() => snapshotEntity(world, entity, names)).toThrow(Error);

        entity.remove(SecretRel(target));
        entity.destroy();
        expect(() => snapshotEntity(world, entity, names)).toThrow(Error);
    });

    it('snapshots every entity except the internal world entity', () => {
        const names = registry();
        const Time = trait({ delta: 0 });
        world.add(Time);
        world.set(Time, { delta: 4 });

        const a = world.spawn(Position({ x: 1, y: 1 }));
        const b = world.spawn(Dead);
        const snapshot = snapshotWorld(world, names);

        expect(snapshot.entities.map((entry) => entry.id)).toEqual([a.id(), b.id()]);
        expect(snapshot.entities.some((entry) => entry.id === world.entities[0]!.id())).toBe(false);
        expect(JSON.stringify(snapshot)).not.toContain('delta');
    });

    it('rolls an entity back to the snapshot traits and relations', () => {
        const names = registry();
        const parent = world.spawn();
        const item = world.spawn();
        const extra = world.spawn();
        const entity = world.spawn(
            Position({ x: 1, y: 2 }),
            Dead,
            ChildOf(parent),
            Contains(item, { amount: 4 })
        );
        const snapshot = snapshotEntity(world, entity, names);

        entity.remove(Dead, ChildOf(parent));
        entity.add(Health, Likes(extra));
        entity.set(Position, { x: 9, y: 9 });
        entity.set(Contains(item), { amount: 1 });

        rollbackEntity(world, entity, names, snapshot);

        expect(entity.has(Dead)).toBe(true);
        expect(entity.has(Health)).toBe(false);
        expect(entity.has(Likes(extra))).toBe(false);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
        expect(entity.has(ChildOf(parent))).toBe(true);
        expect(entity.targetFor(ChildOf)).toBe(parent);
        expect(entity.get(Contains(item))).toEqual({ amount: 4, meta: { quality: 1 } });
        expect(world.has(entity)).toBe(true);
        expect(world.has(parent)).toBe(true);
    });

    it('replaces relation targets and keeps relation data independent after rollback', () => {
        const names = registry();
        const parent = world.spawn();
        const nextParent = world.spawn();
        const item = world.spawn();
        const entity = world.spawn(ChildOf(parent), Contains(item, { amount: 2 }));
        const snapshot = entity.snapshot(names);

        entity.add(ChildOf(nextParent));
        entity.set(Contains(item), { amount: 8 });
        entity.rollback(names, snapshot);

        expect(entity.targetFor(ChildOf)).toBe(parent);
        expect(entity.has(ChildOf(nextParent))).toBe(false);
        expect(entity.get(Contains(item))!.amount).toBe(2);

        const restored = snapshot.relations?.contains?.[0].data as { amount: number };
        restored.amount = 50;
        expect(entity.get(Contains(item))!.amount).toBe(2);
    });

    it('throws when rolling back a destroyed entity, an unknown key, or a missing target', () => {
        const names = registry();
        const entity = world.spawn(Position({ x: 1, y: 1 }));
        const snapshot = snapshotEntity(world, entity, names);

        const unknown = {
            id: entity.id(),
            traits: { nope: { x: 1 } },
        } as EntitySnapshot;
        expect(() => rollbackEntity(world, entity, names, unknown)).toThrow(Error);
        expect(entity.get(Position)).toEqual({ x: 1, y: 1 });

        const dangling = {
            id: entity.id(),
            traits: {},
            relations: { childOf: [{ targetId: 999 }] },
        } as EntitySnapshot;
        expect(() => entity.rollback(names, dangling)).toThrow(Error);
        expect(entity.has(Position)).toBe(true);

        entity.destroy();
        expect(() => rollbackEntity(world, entity, names, snapshot)).toThrow(Error);
    });

    it('replaces world state and recreates the same entity ids', () => {
        const names = registry();
        const parent = world.spawn(Position({ x: 1, y: 0 }), Dead);
        const child = world.spawn(Health({ value: 3 }), ChildOf(parent));
        const removed = world.spawn(Position({ x: 5, y: 5 }));
        removed.destroy();
        const checkpoint = world.snapshot(names);

        parent.set(Position, { x: 20, y: 20 });
        parent.remove(Dead);
        child.remove(ChildOf(parent));
        const intruder = world.spawn(Health({ value: 99 }));

        world.rollback(names, checkpoint);

        expect(world.has(parent)).toBe(true);
        expect(world.has(child)).toBe(true);
        expect(world.has(intruder)).toBe(false);
        expect(world.has(removed)).toBe(false);
        expect(parent.id()).toBe(checkpoint.entities[0]!.id);
        expect(child.id()).toBe(checkpoint.entities[1]!.id);
        expect(parent.get(Position)).toEqual({ x: 1, y: 0 });
        expect(parent.has(Dead)).toBe(true);
        expect(child.get(Health)).toEqual({ value: 3, meta: { regen: 1 } });
        expect(child.targetFor(ChildOf)).toBe(parent);
        expect(world.query(Position)).toContain(parent);
        expect(world.query(Dead)).toContain(parent);
        expect(world.query(ChildOf(parent))).toContain(child);

        const ids = world.entities.map((entity) => entity.id()).sort((a, b) => a - b);
        expect(ids).toEqual([0, parent.id(), child.id()]);

        expect(snapshotWorld(world, names)).toEqual(checkpoint);
    });

    it('recreates sparse entity ids and leaves a failed world rollback untouched', () => {
        const names = registry();
        const first = world.spawn(Position({ x: 1, y: 1 }));
        const gap = world.spawn(Position({ x: 2, y: 2 }));
        const third = world.spawn(Position({ x: 3, y: 3 }));
        gap.destroy();
        const checkpoint = snapshotWorld(world, names);

        first.set(Position, { x: 8, y: 8 });
        const unknown = {
            entities: [{ id: first.id(), traits: { missing: true } }],
        } as WorldSnapshot;
        expect(() => rollbackWorld(world, names, unknown)).toThrow(Error);
        expect(first.get(Position)).toEqual({ x: 8, y: 8 });
        expect(world.has(third)).toBe(true);

        const dangling = {
            entities: [
                {
                    id: first.id(),
                    traits: {},
                    relations: { likes: [{ targetId: 40 }] },
                },
            ],
        } as WorldSnapshot;
        expect(() => world.rollback(names, dangling)).toThrow(Error);
        expect(world.has(first)).toBe(true);
        expect(world.has(third)).toBe(true);

        rollbackWorld(world, names, checkpoint);
        expect(world.has(first)).toBe(true);
        expect(world.has(gap)).toBe(false);
        expect(world.has(third)).toBe(true);
        expect(first.get(Position)).toEqual({ x: 1, y: 1 });
        expect(third.get(Position)).toEqual({ x: 3, y: 3 });
        expect(
            world.entities
                .map((entity) => entity.id())
                .filter((id) => id !== 0)
                .sort((a, b) => a - b)
        ).toEqual([first.id(), third.id()]);
    });

    it('restores relations that target the world entity', () => {
        const names = registry();
        const anchor = world.entities[0]!;
        const entity = world.spawn(ChildOf(anchor));
        const checkpoint = snapshotWorld(world, names);

        entity.remove(ChildOf(anchor));
        rollbackWorld(world, names, checkpoint);

        expect(entity.has(ChildOf(anchor))).toBe(true);
        expect(entity.targetFor(ChildOf)).toBe(anchor);
    });

    it('diffs entity traits with shallow equality and sorted names', () => {
        const nested = { z: 1 };
        const diff = diffEntitySnapshots(
            { id: 1, traits: { zeta: true, pos: { x: 1, nested }, mid: { n: 1 } } },
            { id: 1, traits: { mid: { n: 1 }, alpha: { v: 2 }, pos: { x: 1, nested: { z: 1 } } } }
        );

        expect(diff).toEqual({
            addedTraits: ['alpha'],
            removedTraits: ['zeta'],
            changedTraits: ['pos'],
        });

        const same = diffEntitySnapshots(
            { id: 1, traits: { b: { n: 1 }, a: true } },
            { id: 4, traits: { a: true, b: { n: 1 } } }
        );
        expect(same).toEqual({ addedTraits: [], removedTraits: [], changedTraits: [] });

        const shared = diffEntitySnapshots(
            { id: 1, traits: { pos: { nested } } },
            { id: 1, traits: { pos: { nested } } }
        );
        expect(shared.changedTraits).toEqual([]);

        expect(() => diffEntitySnapshots(undefined as never, { id: 1, traits: {} })).toThrow(Error);
        expect(() => diffEntitySnapshots({ id: 1, traits: {} }, null as never)).toThrow(Error);
    });

    it('diffs world membership without caring about key or target order', () => {
        const before: WorldSnapshot = {
            entities: [
                {
                    id: 2,
                    traits: { b: { n: 1 }, a: true },
                    relations: {
                        likes: [{ targetId: 4 }, { targetId: 2, data: { amount: 1 } }],
                        childOf: [{ targetId: 4 }],
                    },
                },
                { id: 10, traits: { a: true }, relations: {} },
                { id: 4, traits: {}, relations: {} },
            ],
        };
        const after: WorldSnapshot = {
            entities: [
                { id: 4, traits: {} },
                {
                    id: 2,
                    traits: { a: true, b: { n: 1 } },
                    relations: {
                        childOf: [{ targetId: 4 }],
                        likes: [{ targetId: 2, data: { amount: 1 } }, { targetId: 4 }],
                    },
                },
                { id: 3, traits: { a: true } },
            ],
        };

        expect(diffWorldSnapshots(before, after)).toEqual({
            added: [3],
            removed: [10],
            changed: [],
        });

        const changed = diffWorldSnapshots(
            { entities: [{ id: 2, traits: { a: { n: 1 } } }] },
            { entities: [{ id: 2, traits: { a: { n: 2 } } }] }
        );
        expect(changed.changed).toEqual([2]);
        expect(
            diffWorldSnapshots(
                { entities: [] },
                {
                    entities: [
                        { id: 10, traits: {} },
                        { id: 2, traits: {} },
                    ],
                }
            ).added
        ).toEqual([2, 10]);

        expect(() => diffWorldSnapshots(null as never, { entities: [] })).toThrow(Error);
        expect(() => diffWorldSnapshots({ entities: [] }, undefined as never)).toThrow(Error);
        expect(() => diffWorldSnapshots({} as WorldSnapshot, { entities: [] })).toThrow(Error);
        expect(() => diffWorldSnapshots({ entities: null } as never, { entities: [] })).toThrow(
            Error
        );
    });

    it('stores entity ids rather than packed entity numbers', () => {
        const names = registry();
        const other = createWorld();
        const entity = other.spawn(Position({ x: 1, y: 2 }));

        expect(other.id).not.toBe(0);
        expect(entity).not.toBe(entity.id());
        expect(snapshotEntity(other, entity, names).id).toBe(entity.id());

        const checkpoint = other.snapshot(names);
        entity.set(Position, { x: 5, y: 5 });
        other.rollback(names, checkpoint);

        expect(entity.isAlive()).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
        other.destroy();
    });

    it('deep copies array values and AoS relation payloads', () => {
        const Inventory = trait({ slots: () => [{ id: 1 }] });
        const Buff = relation({ exclusive: true, store: () => new Vec(1, 2) });
        const names = createTraitRegistry(['inventory', Inventory], ['buff', Buff]);
        const target = world.spawn();
        const entity = world.spawn(Inventory);
        entity.add(Buff(target));
        entity.set(Buff(target), new Vec(1, 2));

        const snapshot = snapshotEntity(world, entity, names);
        const slots = (snapshot.traits.inventory as { slots: Array<{ id: number }> }).slots;
        const buff = snapshot.relations?.buff?.[0].data as Vec;

        slots.push({ id: 2 });
        slots[0].id = 9;
        buff.x = 7;
        expect(entity.get(Inventory)!.slots).toEqual([{ id: 1 }]);
        expect(entity.get(Buff(target))).toEqual({ x: 1, y: 2 });
        expect(buff).toBeInstanceOf(Vec);

        entity.get(Inventory)!.slots.push({ id: 3 });
        expect(slots).toEqual([{ id: 9 }, { id: 2 }]);

        rollbackEntity(world, entity, names, snapshot);
        expect(entity.get(Inventory)!.slots).toEqual([{ id: 9 }, { id: 2 }]);
        expect(entity.get(Inventory)!.slots).not.toBe(slots);
        expect(entity.get(Buff(target))).toBeInstanceOf(Vec);
        expect((entity.get(Buff(target)) as Vec).x).toBe(7);
        expect(entity.get(Buff(target))).not.toBe(buff);
    });

    it('removes relations without cascade-destroying and can clear a world', () => {
        const Orphan = relation({ autoDestroy: 'orphan', exclusive: true });
        const names = createTraitRegistry(['position', Position], ['orphan', Orphan]);
        const parent = world.spawn();
        const child = world.spawn(Position({ x: 1, y: 1 }), Orphan(parent));

        rollbackEntity(world, child, names, { id: child.id(), traits: { position: { x: 1, y: 1 } } });

        expect(world.has(child)).toBe(true);
        expect(world.has(parent)).toBe(true);
        expect(child.has(Orphan(parent))).toBe(false);

        rollbackWorld(world, names, { entities: [] });
        expect(world.entities.map((entity) => entity.id())).toEqual([0]);
    });
});
