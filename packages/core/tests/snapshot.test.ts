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
} from '../src';

class Item {
    constructor(public name = 'hat') {}
}

const Position = trait({ x: 0, y: 0 });
const Health = trait({ hp: 0 });
const Tag = trait();
const Inventory = trait({ items: () => ['sword'] as string[] });
const Body = trait(() => ({ pos: { x: 1 }, name: 'a' }));
const Gear = trait(() => ({ item: new Item('hat') }));
const Likes = relation();
const ChildOf = relation({ store: { order: 0 } });
const Contains = relation({ exclusive: true, store: { amount: 0 } });

describe('Snapshot', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('throws when registry keys, traits, or relations are duplicated', () => {
        expect(() => createTraitRegistry(['pos', Position], ['pos', Health])).toThrow(
            /duplicate key/i
        );
        expect(() => createTraitRegistry(['a', Position], ['b', Position])).toThrow(
            /duplicate trait/i
        );
        expect(() => createTraitRegistry(['a', ChildOf], ['b', ChildOf])).toThrow(
            /duplicate relation/i
        );
        expect(createTraitRegistry(['pos', Position], ['child', ChildOf]).keys()).toEqual([
            'pos',
            'child',
        ]);
    });

    it('snapshots tags, data, and relations', () => {
        const registry = createTraitRegistry(
            ['position', Position],
            ['tag', Tag],
            ['inventory', Inventory],
            ['body', Body],
            ['gear', Gear],
            ['likes', Likes],
            ['child', ChildOf]
        );

        const parent = world.spawn();
        const other = world.spawn();
        const entity = world.spawn(
            Position({ x: 2, y: 4 }),
            Tag,
            Inventory,
            Body,
            Gear,
            Likes(other),
            ChildOf(parent, { order: 3 })
        );

        const snapshot = snapshotEntity(world, entity, registry);

        expect(snapshot.id).toBe(entity.id());
        expect(snapshot.traits.tag).toBe(true);
        expect(snapshot.traits.position).toEqual({ x: 2, y: 4 });
        expect(snapshot.traits.position).not.toBe(entity.get(Position));
        expect(snapshot.traits.body).toEqual({ pos: { x: 1 }, name: 'a' });
        expect((snapshot.traits.body as { pos: { x: number } }).pos).not.toBe(entity.get(Body)!.pos);
        expect((snapshot.traits.gear as { item: Item }).item).toBeInstanceOf(Item);
        expect((snapshot.traits.gear as { item: Item }).item).not.toBe(entity.get(Gear)!.item);
        expect(snapshot.relations?.likes).toEqual([{ targetId: other.id() }]);
        expect(snapshot.relations?.likes?.[0]).not.toHaveProperty('data');
        expect(snapshot.relations?.child).toEqual([{ targetId: parent.id(), data: { order: 3 } }]);

        entity.get(Inventory)!.items.push('shield');
        entity.get(Body)!.pos.x = 9;

        expect(snapshot.traits.inventory).toEqual({ items: ['sword'] });
        expect(snapshot.traits.body).toEqual({ pos: { x: 1 }, name: 'a' });

        (snapshot.traits.inventory as { items: string[] }).items.push('bow');
        const childData = snapshot.relations!.child[0].data as { order: number };
        childData.order = 8;

        expect(entity.get(Inventory)!.items).toEqual(['sword', 'shield']);
        expect(entity.get(ChildOf(parent))!.order).toBe(3);
    });

    it('omits relations when the entity has none', () => {
        const registry = createTraitRegistry(['position', Position]);
        const entity = world.spawn(Position({ x: 1, y: 1 }));
        const snapshot = entity.snapshot(registry);

        expect(snapshot.traits).toEqual({ position: { x: 1, y: 1 } });
        expect(snapshot).not.toHaveProperty('relations');
    });

    it('throws for destroyed entities and unregistered traits or relations', () => {
        const registry = createTraitRegistry(['position', Position]);
        const entity = world.spawn(Position, Likes(world.spawn()));

        expect(() => snapshotEntity(world, entity, registry)).toThrow(/unregistered relation/i);

        const tagged = world.spawn(Position, Tag);
        expect(() => snapshotEntity(world, tagged, registry)).toThrow(/unregistered trait/i);

        const plain = world.spawn(Position);
        plain.destroy();
        expect(() => snapshotEntity(world, plain, registry)).toThrow(/destroyed/i);
        expect(() => rollbackEntity(world, plain, registry, { id: plain.id(), traits: {} })).toThrow(
            /destroyed/i
        );
    });

    it('excludes the internal world entity from world snapshots', () => {
        const registry = createTraitRegistry(['position', Position]);
        world.add(Position({ x: 7, y: 8 }));
        const entity = world.spawn(Position({ x: 1, y: 2 }));

        const snapshot = world.snapshot(registry);

        expect(snapshot.entities.map((entry) => entry.id)).toEqual([entity.id()]);
        expect(snapshot.entities.some((entry) => entry.id === 0)).toBe(false);
    });

    it('rolls an entity back to its snapshot', () => {
        const registry = createTraitRegistry(
            ['position', Position],
            ['health', Health],
            ['tag', Tag],
            ['likes', Likes],
            ['contains', Contains]
        );

        const a = world.spawn();
        const b = world.spawn();
        const entity = world.spawn(Position({ x: 1, y: 2 }), Tag, Contains(a, { amount: 4 }));
        const snapshot = snapshotEntity(world, entity, registry);

        entity.set(Position, { x: 9, y: 9 });
        entity.remove(Tag);
        entity.add(Health({ hp: 3 }), Likes(b));
        entity.add(Contains(b, { amount: 1 }));

        entity.rollback(registry, snapshot);

        expect(entity.has(Tag)).toBe(true);
        expect(entity.has(Health)).toBe(false);
        expect(entity.has(Likes(b))).toBe(false);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
        expect(entity.targetFor(Contains)).toBe(a);
        expect(entity.get(Contains(a))).toEqual({ amount: 4 });
        expect(entity.get(Position)).not.toBe(snapshot.traits.position);
    });

    it('throws when a rollback target is missing or a registry key is unknown', () => {
        const registry = createTraitRegistry(['position', Position], ['likes', Likes]);
        const target = world.spawn();
        const entity = world.spawn(Position({ x: 1, y: 1 }), Likes(target));
        const snapshot = snapshotEntity(world, entity, registry);

        target.destroy();
        expect(() => entity.rollback(registry, snapshot)).toThrow(/target entity does not exist/i);
        expect(entity.get(Position)).toEqual({ x: 1, y: 1 });

        const alive = world.spawn(Position({ x: 2, y: 3 }));
        const aliveSnapshot = alive.snapshot(registry);
        alive.set(Position, { x: 8, y: 8 });
        expect(() =>
            alive.rollback(registry, {
                ...aliveSnapshot,
                traits: { ...aliveSnapshot.traits, missing: { n: 1 } },
            })
        ).toThrow(/unknown registry key/i);
        expect(alive.get(Position)).toEqual({ x: 8, y: 8 });
    });

    it('replaces world state and recreates checkpoint ids', () => {
        const registry = createTraitRegistry(
            ['position', Position],
            ['health', Health],
            ['child', ChildOf]
        );

        const parent = world.spawn(Health({ hp: 2 }));
        const child = world.spawn(Position({ x: 3, y: 4 }), ChildOf(parent, { order: 1 }));
        const checkpoint = snapshotWorld(world, registry);
        const parentId = parent.id();
        const childId = child.id();

        child.destroy();
        parent.set(Health, { hp: 99 });
        const extra = world.spawn(Position({ x: 5, y: 5 }));

        rollbackWorld(world, registry, checkpoint);

        expect(extra.isAlive()).toBe(false);
        expect(parent.isAlive()).toBe(true);
        expect(child.isAlive()).toBe(true);
        expect(parent.id()).toBe(parentId);
        expect(child.id()).toBe(childId);
        expect(parent.get(Health)).toEqual({ hp: 2 });
        expect(child.get(Position)).toEqual({ x: 3, y: 4 });
        expect(child.get(ChildOf(parent))).toEqual({ order: 1 });
        expect([...world.query(Position)].map((entity) => entity.id())).toEqual([childId]);

        const hole = world.spawn();
        hole.destroy();
        const gapped = snapshotWorld(world, registry);
        world.spawn(Health({ hp: 1 }));
        world.rollback(registry, gapped);
        expect(
            world.entities
                .filter((entity) => entity.id() !== 0)
                .map((entity) => entity.id())
                .sort((a, b) => a - b)
        ).toEqual(gapped.entities.map((entity) => entity.id).sort((a, b) => a - b));
    });

    it('rejects world rollback with unknown keys or dangling targets before mutating', () => {
        const registry = createTraitRegistry(['position', Position], ['child', ChildOf]);
        const entity = world.spawn(Position({ x: 1, y: 2 }));
        const checkpoint = snapshotWorld(world, registry);

        expect(() =>
            world.rollback(registry, {
                entities: [{ id: entity.id(), traits: { missing: true } }],
            })
        ).toThrow(/unknown registry key/i);
        expect(entity.isAlive()).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });

        expect(() =>
            rollbackWorld(world, registry, {
                entities: [
                    {
                        id: entity.id(),
                        traits: checkpoint.entities[0].traits,
                        relations: { child: [{ targetId: 999, data: { order: 1 } }] },
                    },
                ],
            })
        ).toThrow(/target entity does not exist/i);
        expect(entity.isAlive()).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
    });

    it('restores relations that target the world entity', () => {
        const registry = createTraitRegistry(['likes', Likes]);
        const worldEntity = world.entities[0]!;
        const entity = world.spawn(Likes(worldEntity));
        const checkpoint = snapshotWorld(world, registry);

        entity.remove(Likes(worldEntity));
        world.rollback(registry, checkpoint);

        const restored = world.entities.find((candidate) => candidate.id() === entity.id())!;
        expect(restored.has(Likes(world.entities[0]!))).toBe(true);
    });

    it('diffs entity traits with shallow equality and sorted keys', () => {
        const nested = { z: 1 };
        const before = {
            id: 1,
            traits: { b: true, a: { n: 1, nested }, z: { n: 1 } },
        };
        const after = {
            id: 1,
            traits: { c: true, a: { n: 2, nested: { z: 1 } }, d: { n: 1 }, z: { n: 1 } },
        };

        expect(diffEntitySnapshots(before, after)).toEqual({
            addedTraits: ['c', 'd'],
            removedTraits: ['b'],
            changedTraits: ['a'],
        });

        expect(
            diffEntitySnapshots(
                { id: 1, traits: { a: { nested } } },
                { id: 1, traits: { a: { nested } } }
            ).changedTraits
        ).toEqual([]);

        expect(() => diffEntitySnapshots(null as never, before)).toThrow(Error);
        expect(() => diffEntitySnapshots(before, undefined as never)).toThrow(Error);
    });

    it('diffs worlds independent of key and target order', () => {
        const shared = { order: 1 };
        const before = {
            entities: [
                {
                    id: 4,
                    traits: { b: true, a: { n: 1 } },
                    relations: {
                        child: [
                            { targetId: 2, data: shared },
                            { targetId: 1, data: { order: 2 } },
                        ],
                        likes: [{ targetId: 3 }],
                    },
                },
                { id: 2, traits: { a: true }, relations: {} },
                { id: 9, traits: {} },
            ],
        };
        const after = {
            entities: [
                { id: 2, traits: { a: true } },
                {
                    id: 4,
                    traits: { a: { n: 1 }, b: true },
                    relations: {
                        likes: [{ targetId: 3 }],
                        child: [
                            { targetId: 1, data: { order: 2 } },
                            { targetId: 2, data: shared },
                        ],
                    },
                },
                { id: 1, traits: { a: true } },
                { id: 7, traits: {} },
            ],
        };

        expect(diffWorldSnapshots(before, after)).toEqual({
            added: [1, 7],
            removed: [9],
            changed: [],
        });

        after.entities[1].relations!.child[1].data = { order: 5 };
        expect(diffWorldSnapshots(before, after).changed).toEqual([4]);

        expect(() => diffWorldSnapshots(null as never, before)).toThrow(Error);
        expect(() => diffWorldSnapshots({} as never, before)).toThrow(Error);
        expect(() => diffWorldSnapshots(before, { entities: null } as never)).toThrow(Error);
    });
});
