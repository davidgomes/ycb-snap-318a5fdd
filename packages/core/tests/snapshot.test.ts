import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    createAdded,
    createTraitRegistry,
    createWorld,
    diffEntitySnapshots,
    diffWorldSnapshots,
    type EntitySnapshot,
    ordered,
    relation,
    rollbackEntity,
    rollbackWorld,
    snapshotEntity,
    snapshotWorld,
    trait,
    type WorldSnapshot,
} from '../src';

class Vec {
    constructor(
        public x = 0,
        public y = 0
    ) {}
    length() {
        return Math.hypot(this.x, this.y);
    }
}

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Inventory = trait({ items: () => [] as string[] });
const Transform = trait(() => ({ position: new Vec(), tags: new Set<string>() }));
const IsPlayer = trait();
const IsEnemy = trait();
const ChildOf = relation();
const Contains = relation({ store: { amount: 0 } });
const Targeting = relation({ exclusive: true });

const registry = createTraitRegistry(
    ['Position', Position],
    ['Velocity', Velocity],
    ['Inventory', Inventory],
    ['Transform', Transform],
    ['IsPlayer', IsPlayer],
    ['IsEnemy', IsEnemy],
    ['ChildOf', ChildOf],
    ['Contains', Contains],
    ['Targeting', Targeting]
);

describe('Snapshots', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    describe('createTraitRegistry', () => {
        it('should accept traits and relations', () => {
            expect(() =>
                createTraitRegistry(['Position', Position], ['ChildOf', ChildOf])
            ).not.toThrow();
            expect(() => createTraitRegistry()).not.toThrow();
        });

        it('should throw on duplicate keys', () => {
            expect(() => createTraitRegistry(['A', Position], ['A', Velocity])).toThrow(Error);
            expect(() => createTraitRegistry(['A', Position], ['A', ChildOf])).toThrow(Error);
        });

        it('should throw on duplicate traits', () => {
            expect(() => createTraitRegistry(['A', Position], ['B', Position])).toThrow(Error);
        });

        it('should throw on duplicate relations', () => {
            expect(() => createTraitRegistry(['A', ChildOf], ['B', ChildOf])).toThrow(Error);
        });

        it('should throw on entries that are not traits or relations', () => {
            // @ts-expect-error - Testing invalid input
            expect(() => createTraitRegistry(['A', {}])).toThrow(Error);
            // @ts-expect-error - Testing invalid input
            expect(() => createTraitRegistry([1, Position])).toThrow(Error);
        });
    });

    describe('snapshotEntity', () => {
        it('should store tags as true and data traits as copies', () => {
            const entity = world.spawn(Position({ x: 1, y: 2 }), IsPlayer);
            const snapshot = snapshotEntity(world, entity, registry);

            expect(snapshot).toEqual({
                id: entity,
                traits: { Position: { x: 1, y: 2 }, IsPlayer: true },
            });
            expect(snapshot).not.toHaveProperty('relations');

            entity.set(Position, { x: 10 });
            expect(snapshot.traits.Position).toEqual({ x: 1, y: 2 });
        });

        it('should deep copy nested and AoS data', () => {
            const entity = world.spawn(Inventory({ items: ['sword'] }), Transform);
            const transform = entity.get(Transform)!;
            transform.position.x = 5;
            transform.tags.add('hero');

            const snapshot = snapshotEntity(world, entity, registry);
            const items = entity.get(Inventory)!.items;
            items.push('shield');
            transform.position.x = 99;
            transform.tags.add('villain');

            const stored = snapshot.traits.Transform as ReturnType<typeof Transform.schema>;
            expect(snapshot.traits.Inventory).toEqual({ items: ['sword'] });
            expect(stored).not.toBe(transform);
            expect(stored.position).toBeInstanceOf(Vec);
            expect(stored.position.length()).toBe(5);
            expect([...stored.tags]).toEqual(['hero']);
        });

        it('should store relations with deep copied data only when the relation has a store', () => {
            const parent = world.spawn();
            const chest = world.spawn();
            const entity = world.spawn(ChildOf(parent), Contains(chest, { amount: 3 }));

            const snapshot = snapshotEntity(world, entity, registry);

            expect(snapshot.relations).toEqual({
                ChildOf: [{ targetId: parent }],
                Contains: [{ targetId: chest, data: { amount: 3 } }],
            });
            expect(snapshot.relations!.ChildOf[0]).not.toHaveProperty('data');

            entity.set(Contains(chest), { amount: 7 });
            expect(snapshot.relations!.Contains[0].data).toEqual({ amount: 3 });
        });

        it('should throw for destroyed entities', () => {
            const entity = world.spawn(Position);
            entity.destroy();
            expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);
        });

        it('should throw for unregistered traits and relations', () => {
            const Unknown = trait({ value: 0 });
            const UnknownRelation = relation();

            const withTrait = world.spawn(Position, Unknown);
            expect(() => snapshotEntity(world, withTrait, registry)).toThrow(Error);

            const withRelation = world.spawn(Position, UnknownRelation(world.spawn()));
            expect(() => snapshotEntity(world, withRelation, registry)).toThrow(Error);
        });

        it('should be available as an entity method', () => {
            const entity = world.spawn(Position({ x: 4 }));
            expect(entity.snapshot(registry)).toEqual(snapshotEntity(world, entity, registry));
        });
    });

    describe('snapshotWorld', () => {
        it('should snapshot every entity except the world entity', () => {
            world.add(Position({ x: 100 }));
            const a = world.spawn(Position({ x: 1 }));
            const b = world.spawn(IsEnemy);

            const checkpoint = snapshotWorld(world, registry);

            expect(checkpoint).toEqual({
                entities: [
                    { id: a, traits: { Position: { x: 1, y: 0 } } },
                    { id: b, traits: { IsEnemy: true } },
                ],
            });
            expect(world.snapshot(registry)).toEqual(checkpoint);
        });

        it('should return no entities for an empty world', () => {
            expect(snapshotWorld(world, registry)).toEqual({ entities: [] });
        });
    });

    describe('rollbackEntity', () => {
        it('should restore traits exactly', () => {
            const entity = world.spawn(Position({ x: 1, y: 2 }), IsPlayer);
            const snapshot = snapshotEntity(world, entity, registry);

            entity.set(Position, { x: 50 });
            entity.remove(IsPlayer);
            entity.add(Velocity, IsEnemy);

            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
            expect(entity.has(IsPlayer)).toBe(true);
            expect(entity.has(Velocity)).toBe(false);
            expect(entity.has(IsEnemy)).toBe(false);
            expect(snapshotEntity(world, entity, registry)).toEqual(snapshot);
        });

        it('should remove traits that are not registered', () => {
            const Unknown = trait();
            const entity = world.spawn(Position);
            const snapshot = snapshotEntity(world, entity, registry);

            entity.add(Unknown);
            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.has(Unknown)).toBe(false);
        });

        it('should restore relations and relation data', () => {
            const a = world.spawn();
            const b = world.spawn();
            const c = world.spawn();
            const entity = world.spawn(
                ChildOf(a),
                ChildOf(b),
                Contains(a, { amount: 1 }),
                Targeting(a)
            );
            const snapshot = snapshotEntity(world, entity, registry);

            entity.remove(ChildOf(a));
            entity.add(ChildOf(c));
            entity.set(Contains(a), { amount: 9 });
            entity.add(Contains(b, { amount: 2 }));
            entity.add(Targeting(c));

            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.targetsFor(ChildOf).sort()).toEqual([a, b].sort());
            expect(entity.targetsFor(Contains)).toEqual([a]);
            expect(entity.get(Contains(a))).toEqual({ amount: 1 });
            expect(entity.targetFor(Targeting)).toBe(a);
            expect(world.query(ChildOf(c))).not.toContain(entity);
        });

        it('should remove all relations when the snapshot has none', () => {
            const target = world.spawn();
            const entity = world.spawn(Position);
            const snapshot = snapshotEntity(world, entity, registry);

            entity.add(ChildOf(target), Contains(target));
            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.has(ChildOf('*'))).toBe(false);
            expect(entity.has(Contains('*'))).toBe(false);
            expect(snapshotEntity(world, entity, registry)).not.toHaveProperty('relations');
        });

        it('should not share data with the snapshot', () => {
            const entity = world.spawn(Inventory({ items: ['sword'] }));
            const snapshot = snapshotEntity(world, entity, registry);

            entity.set(Inventory, { items: [] });
            rollbackEntity(world, entity, registry, snapshot);
            entity.get(Inventory)!.items.push('shield');
            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.get(Inventory)).toEqual({ items: ['sword'] });
        });

        it('should only emit change events for traits that changed', () => {
            const entity = world.spawn(Position({ x: 1 }), Velocity({ x: 1 }));
            const snapshot = snapshotEntity(world, entity, registry);
            const onPositionChange = vi.fn();
            const onVelocityChange = vi.fn();
            world.onChange(Position, onPositionChange);
            world.onChange(Velocity, onVelocityChange);

            entity.set(Position, { x: 2 }, false);
            rollbackEntity(world, entity, registry, snapshot);

            expect(onPositionChange).toHaveBeenCalledTimes(1);
            expect(onVelocityChange).not.toHaveBeenCalled();
        });

        it('should throw if a relation target does not exist', () => {
            const target = world.spawn();
            const entity = world.spawn(ChildOf(target), Position({ x: 1 }));
            const snapshot = snapshotEntity(world, entity, registry);

            target.destroy();
            entity.set(Position, { x: 2 });

            expect(() => rollbackEntity(world, entity, registry, snapshot)).toThrow(Error);
            expect(entity.get(Position)).toEqual({ x: 2, y: 0 });
        });

        it('should throw for destroyed entities', () => {
            const entity = world.spawn(Position);
            const snapshot = snapshotEntity(world, entity, registry);
            entity.destroy();

            expect(() => rollbackEntity(world, entity, registry, snapshot)).toThrow(Error);
        });

        it('should throw for unknown registry keys without modifying the entity', () => {
            const entity = world.spawn(Position({ x: 1 }));
            const unknownTrait: EntitySnapshot = { id: entity, traits: { Missing: true } };
            const relationAsTrait: EntitySnapshot = { id: entity, traits: { ChildOf: true } };
            const unknownRelation: EntitySnapshot = {
                id: entity,
                traits: {},
                relations: { Missing: [{ targetId: entity }] },
            };

            expect(() => rollbackEntity(world, entity, registry, unknownTrait)).toThrow(Error);
            expect(() => rollbackEntity(world, entity, registry, relationAsTrait)).toThrow(Error);
            expect(() => rollbackEntity(world, entity, registry, unknownRelation)).toThrow(Error);
            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('should be available as an entity method', () => {
            const entity = world.spawn(Position({ x: 1 }));
            const snapshot = entity.snapshot(registry);

            entity.set(Position, { x: 2 });
            entity.rollback(registry, snapshot);

            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });
    });

    describe('rollbackWorld', () => {
        it('should replace the world state with the checkpoint', () => {
            const a = world.spawn(Position({ x: 1 }), IsPlayer);
            const b = world.spawn(Velocity({ y: 3 }), ChildOf(a));
            const checkpoint = snapshotWorld(world, registry);

            a.set(Position, { x: 10 });
            b.destroy();
            const c = world.spawn(IsEnemy);

            rollbackWorld(world, registry, checkpoint);

            expect(world.has(a)).toBe(true);
            expect(world.has(b)).toBe(true);
            expect(world.has(c)).toBe(false);
            expect(a.get(Position)).toEqual({ x: 1, y: 0 });
            expect(b.get(Velocity)).toEqual({ x: 0, y: 3 });
            expect(b.targetFor(ChildOf)).toBe(a);
            expect(world.query(IsEnemy)).toHaveLength(0);
            expect([...world.query(Position)]).toEqual([a]);
            expect(snapshotWorld(world, registry)).toEqual(checkpoint);
        });

        it('should revive the exact entities even when their IDs were recycled', () => {
            const a = world.spawn(Position({ x: 1 }));
            const checkpoint = snapshotWorld(world, registry);

            a.destroy();
            const recycled = world.spawn(Position({ x: 2 }));
            expect(recycled.id()).toBe(a.id());

            rollbackWorld(world, registry, checkpoint);

            expect(a.isAlive()).toBe(true);
            expect(recycled.isAlive()).toBe(false);
            expect(a.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('should restore entities with generations and skipped IDs', () => {
            const a = world.spawn();
            a.destroy();
            const b = world.spawn(Position({ x: 1 }));
            world.spawn();
            const c = world.spawn(IsPlayer, ChildOf(b));
            const checkpoint = snapshotWorld(world, registry);
            const expected = checkpoint.entities.find((snapshot) => snapshot.id === c)!;

            world.reset();
            rollbackWorld(world, registry, { entities: [expected, checkpoint.entities[0]] });

            expect(b.generation()).toBe(1);
            expect(b.get(Position)).toEqual({ x: 1, y: 0 });
            expect(c.targetFor(ChildOf)).toBe(b);

            // Skipped IDs are free to be used by new entities.
            const spawned = [world.spawn(), world.spawn(), world.spawn()];
            expect(new Set(world.entities).size).toBe(world.entities.length);
            for (const entity of spawned) expect(entity.isAlive()).toBe(true);
            expect(c.isAlive()).toBe(true);
        });

        it('should keep world traits and subscriptions', () => {
            world.add(Position({ x: 42 }));
            const entity = world.spawn(Position({ x: 1 }));
            const checkpoint = snapshotWorld(world, registry);
            const onAdd = vi.fn();
            world.onAdd(Position, onAdd);

            entity.destroy();
            rollbackWorld(world, registry, checkpoint);

            expect(world.get(Position)).toEqual({ x: 42, y: 0 });
            expect(onAdd).toHaveBeenCalledWith(entity);
        });

        it('should update tracking queries', () => {
            const Added = createAdded();
            const entity = world.spawn(Position);
            const checkpoint = snapshotWorld(world, registry);
            world.query(Added(Position));

            entity.destroy();
            rollbackWorld(world, registry, checkpoint);

            expect([...world.query(Added(Position))]).toEqual([entity]);
        });

        it('should restore into another world', () => {
            const parent = world.spawn(Position({ x: 1 }));
            world.spawn(ChildOf(parent), Contains(parent, { amount: 5 }));
            const checkpoint = snapshotWorld(world, registry);

            const other = createWorld();
            other.spawn(IsEnemy);
            rollbackWorld(other, registry, checkpoint);

            expect(snapshotWorld(other, registry)).toEqual(checkpoint);
            expect(other.query(IsEnemy)).toHaveLength(0);
            expect(other.query(ChildOf('*'))[0].targetFor(ChildOf)!.get(Position)).toEqual({
                x: 1,
                y: 0,
            });
            other.destroy();
        });

        it('should support JSON round trips', () => {
            const parent = world.spawn(Inventory({ items: ['map'] }));
            world.spawn(ChildOf(parent), Contains(parent, { amount: 2 }), IsPlayer);
            const checkpoint = snapshotWorld(world, registry);

            world.reset();
            rollbackWorld(world, registry, JSON.parse(JSON.stringify(checkpoint)));

            expect(snapshotWorld(world, registry)).toEqual(checkpoint);
        });

        it('should restore ordered relations in order', () => {
            const OrderedChildren = ordered(ChildOf);
            const orderedRegistry = createTraitRegistry(
                ['ChildOf', ChildOf],
                ['OrderedChildren', OrderedChildren]
            );

            const parent = world.spawn(OrderedChildren);
            const a = world.spawn();
            const b = world.spawn();
            const c = world.spawn();
            parent.get(OrderedChildren)!.push(c, a, b);
            const checkpoint = snapshotWorld(world, orderedRegistry);

            parent.get(OrderedChildren)!.splice(0, 1);
            parent.get(OrderedChildren)!.reverse();

            rollbackWorld(world, orderedRegistry, checkpoint);

            expect([...parent.get(OrderedChildren)!]).toEqual([c, a, b]);
            expect(c.targetFor(ChildOf)).toBe(parent);
            expect(snapshotWorld(world, orderedRegistry)).toEqual(checkpoint);

            parent.get(OrderedChildren)!.reverse();
            rollbackEntity(world, parent, orderedRegistry, checkpoint.entities[0]);
            expect([...parent.get(OrderedChildren)!]).toEqual([c, a, b]);
        });

        it('should throw for unknown registry keys without modifying the world', () => {
            const entity = world.spawn(Position({ x: 1 }));
            const checkpoint: WorldSnapshot = { entities: [{ id: 5, traits: { Missing: true } }] };

            expect(() => rollbackWorld(world, registry, checkpoint)).toThrow(Error);
            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('should throw for dangling relation targets without modifying the world', () => {
            const entity = world.spawn(Position({ x: 1 }));
            const checkpoint: WorldSnapshot = {
                entities: [{ id: 5, traits: {}, relations: { ChildOf: [{ targetId: entity }] } }],
            };

            expect(() => rollbackWorld(world, registry, checkpoint)).toThrow(Error);
            expect(entity.isAlive()).toBe(true);
        });

        it('should throw for invalid checkpoints', () => {
            // @ts-expect-error - Testing invalid input
            expect(() => rollbackWorld(world, registry, null)).toThrow(Error);
            // @ts-expect-error - Testing invalid input
            expect(() => rollbackWorld(world, registry, {})).toThrow(Error);

            const duplicate = { id: 1, traits: {} };
            expect(() =>
                rollbackWorld(world, registry, { entities: [duplicate, duplicate] })
            ).toThrow(Error);
            expect(() =>
                rollbackWorld(world, registry, { entities: [{ id: 0, traits: {} }] })
            ).toThrow(Error);
        });

        it('should be available as a world method', () => {
            const entity = world.spawn(Position({ x: 1 }));
            const checkpoint = world.snapshot(registry);

            entity.destroy();
            world.rollback(registry, checkpoint);

            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });
    });

    describe('diffEntitySnapshots', () => {
        it('should report sorted added, removed and changed traits', () => {
            const a: EntitySnapshot = {
                id: 1,
                traits: { Velocity: { x: 0 }, Position: { x: 0 }, IsPlayer: true, Health: { hp: 1 } },
            };
            const b: EntitySnapshot = {
                id: 1,
                traits: {
                    Position: { x: 1 },
                    IsPlayer: true,
                    Health: { hp: 1 },
                    IsEnemy: true,
                    Armor: {},
                },
            };

            expect(diffEntitySnapshots(a, b)).toEqual({
                addedTraits: ['Armor', 'IsEnemy'],
                removedTraits: ['Velocity'],
                changedTraits: ['Position'],
            });
        });

        it('should compare data shallowly', () => {
            const shared = ['sword'];
            const a: EntitySnapshot = { id: 1, traits: { Inventory: { items: shared } } };
            const b: EntitySnapshot = { id: 1, traits: { Inventory: { items: shared } } };
            const c: EntitySnapshot = { id: 1, traits: { Inventory: { items: ['sword'] } } };

            expect(diffEntitySnapshots(a, b).changedTraits).toEqual([]);
            expect(diffEntitySnapshots(a, c).changedTraits).toEqual(['Inventory']);
        });

        it('should diff live snapshots', () => {
            const entity = world.spawn(Position, IsPlayer);
            const before = snapshotEntity(world, entity, registry);
            entity.set(Position, { x: 1 });
            entity.remove(IsPlayer);
            entity.add(Velocity);

            expect(diffEntitySnapshots(before, snapshotEntity(world, entity, registry))).toEqual({
                addedTraits: ['Velocity'],
                removedTraits: ['IsPlayer'],
                changedTraits: ['Position'],
            });
        });

        it('should throw for missing snapshots', () => {
            const snapshot: EntitySnapshot = { id: 1, traits: {} };
            // @ts-expect-error - Testing invalid input
            expect(() => diffEntitySnapshots(null, snapshot)).toThrow(Error);
            // @ts-expect-error - Testing invalid input
            expect(() => diffEntitySnapshots(snapshot, undefined)).toThrow(Error);
        });
    });

    describe('diffWorldSnapshots', () => {
        it('should report sorted added, removed and changed entities', () => {
            const before: WorldSnapshot = {
                entities: [
                    { id: 9, traits: { Position: { x: 0 } } },
                    { id: 3, traits: { IsPlayer: true } },
                    { id: 1, traits: {} },
                    { id: 7, traits: { Position: { x: 0 } } },
                ],
            };
            const after: WorldSnapshot = {
                entities: [
                    { id: 8, traits: {} },
                    { id: 7, traits: { Position: { x: 1 } } },
                    { id: 2, traits: {} },
                    { id: 3, traits: { IsPlayer: true } },
                    { id: 9, traits: { Position: { x: 0 } } },
                ],
            };

            expect(diffWorldSnapshots(before, after)).toEqual({
                added: [2, 8],
                removed: [1],
                changed: [7],
            });
        });

        it('should ignore trait, relation and target ordering', () => {
            const before: WorldSnapshot = {
                entities: [
                    {
                        id: 1,
                        traits: { Position: { x: 1, y: 2 }, IsPlayer: true },
                        relations: {
                            ChildOf: [{ targetId: 2 }, { targetId: 3 }],
                            Contains: [
                                { targetId: 2, data: { amount: 1 } },
                                { targetId: 3, data: { amount: 2 } },
                            ],
                        },
                    },
                ],
            };
            const after: WorldSnapshot = {
                entities: [
                    {
                        id: 1,
                        traits: { IsPlayer: true, Position: { y: 2, x: 1 } },
                        relations: {
                            Contains: [
                                { targetId: 3, data: { amount: 2 } },
                                { targetId: 2, data: { amount: 1 } },
                            ],
                            ChildOf: [{ targetId: 3 }, { targetId: 2 }],
                        },
                    },
                ],
            };

            expect(diffWorldSnapshots(before, after)).toEqual({
                added: [],
                removed: [],
                changed: [],
            });
        });

        it('should detect relation changes', () => {
            const base: EntitySnapshot = {
                id: 1,
                traits: {},
                relations: { Contains: [{ targetId: 2, data: { amount: 1 } }] },
            };
            const diff = (relations: EntitySnapshot['relations']) =>
                diffWorldSnapshots(
                    { entities: [base] },
                    { entities: [{ id: 1, traits: {}, relations }] }
                ).changed;

            expect(diff({ Contains: [{ targetId: 2, data: { amount: 1 } }] })).toEqual([]);
            expect(diff({ Contains: [{ targetId: 2, data: { amount: 5 } }] })).toEqual([1]);
            expect(diff({ Contains: [{ targetId: 3, data: { amount: 1 } }] })).toEqual([1]);
            expect(diff({ ChildOf: [{ targetId: 2 }] })).toEqual([1]);
            expect(diff({})).toEqual([1]);
        });

        it('should treat empty relations as no relations', () => {
            const before: WorldSnapshot = {
                entities: [{ id: 1, traits: { IsPlayer: true }, relations: {} }],
            };
            const after: WorldSnapshot = { entities: [{ id: 1, traits: { IsPlayer: true } }] };

            expect(diffWorldSnapshots(before, after)).toEqual({
                added: [],
                removed: [],
                changed: [],
            });
        });

        it('should diff live world snapshots', () => {
            const a = world.spawn(Position);
            const b = world.spawn(IsPlayer);
            const before = snapshotWorld(world, registry);

            a.set(Position, { x: 1 });
            b.destroy();
            const c = world.spawn(IsEnemy);

            expect(diffWorldSnapshots(before, snapshotWorld(world, registry))).toEqual({
                added: [c],
                removed: [b],
                changed: [a],
            });
        });

        it('should throw for invalid snapshots', () => {
            const snapshot: WorldSnapshot = { entities: [] };
            // @ts-expect-error - Testing invalid input
            expect(() => diffWorldSnapshots(null, snapshot)).toThrow(Error);
            // @ts-expect-error - Testing invalid input
            expect(() => diffWorldSnapshots(snapshot, undefined)).toThrow(Error);
            // @ts-expect-error - Testing invalid input
            expect(() => diffWorldSnapshots({}, snapshot)).toThrow(Error);
            // @ts-expect-error - Testing invalid input
            expect(() => diffWorldSnapshots(snapshot, { entities: {} })).toThrow(Error);
        });
    });
});
