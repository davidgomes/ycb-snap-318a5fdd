import { beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest';
import {
    createAdded,
    createAspect,
    createChanged,
    createRemoved,
    createWorld,
    Not,
    Or,
    ordered,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ vx: 0, vy: 0 });
const Health = trait({ hp: 100 });
const IsPlayer = trait();
const Mesh = trait(() => ({ name: 'mesh', visible: true }));

const Movable = createAspect(Position, Velocity);

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    describe('creation', () => {
        it('exposes id, traits and schema', () => {
            expect(typeof Movable.id).toBe('number');
            expect(Movable.traits).toEqual([Position, Velocity]);
            expect(Movable.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
        });

        it('returns a distinct instance for each call', () => {
            const A = createAspect(Position, Velocity);
            const B = createAspect(Position, Velocity);
            expect(A).not.toBe(B);
            expect(A.id).not.toBe(B.id);
        });

        it('throws on overlapping field names', () => {
            const Other = trait({ x: 1 });
            expect(() => createAspect(Position, Other)).toThrow();
        });

        it('throws on relation constituents', () => {
            const ChildOf = relation();
            expect(() => createAspect(Position, ChildOf as any)).toThrow();
            expect(() => createAspect(Position, ChildOf('*' as any) as any)).toThrow();
            expect(() => createAspect(Position, ordered(ChildOf) as any)).toThrow();
        });

        it('requires at least two traits', () => {
            expect(() => (createAspect as any)(Position)).toThrow();
        });

        it('accepts tag traits', () => {
            const Player = createAspect(Position, IsPlayer);
            expect(Player.traits).toEqual([Position, IsPlayer]);
            expect(Player.schema).toEqual({ x: 0, y: 0 });
        });

        it('flattens nested aspects', () => {
            const Unit = createAspect(Movable, Health);
            expect(Unit.traits).toEqual([Position, Velocity, Health]);
            expect(Unit.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0, hp: 100 });
        });
    });

    describe('entity operations', () => {
        it('has requires every constituent', () => {
            const entity = world.spawn(Position);
            expect(entity.has(Movable)).toBe(false);
            entity.add(Velocity);
            expect(entity.has(Movable)).toBe(true);
        });

        it('get returns merged data or undefined', () => {
            const entity = world.spawn(Position({ x: 1, y: 2 }));
            expect(entity.get(Movable)).toBeUndefined();

            entity.add(Velocity({ vx: 3, vy: 4 }));
            expect(entity.get(Movable)).toEqual({ x: 1, y: 2, vx: 3, vy: 4 });

            expectTypeOf(entity.get(Movable)).toEqualTypeOf<
                { x: number; y: number; vx: number; vy: number } | undefined
            >();
        });

        it('set distributes fields and triggers per-trait change detection', () => {
            const entity = world.spawn(Movable);
            const positionChanged = vi.fn();
            const velocityChanged = vi.fn();
            world.onChange(Position, positionChanged);
            world.onChange(Velocity, velocityChanged);

            entity.set(Movable, { x: 5, vy: 2 });
            expect(entity.get(Position)).toEqual({ x: 5, y: 0 });
            expect(entity.get(Velocity)).toEqual({ vx: 0, vy: 2 });
            expect(positionChanged).toHaveBeenCalledTimes(1);
            expect(velocityChanged).toHaveBeenCalledTimes(1);

            entity.set(Movable, { y: 7 });
            expect(positionChanged).toHaveBeenCalledTimes(2);
            expect(velocityChanged).toHaveBeenCalledTimes(1);

            entity.set(Movable, (prev) => ({ x: prev.x + 1 }));
            expect(entity.get(Position)!.x).toBe(6);
        });

        it('set writes fields onto AoS constituents', () => {
            const Renderable = createAspect(Position, Mesh);
            const entity = world.spawn(Renderable);
            const mesh = entity.get(Mesh)!;

            entity.set(Renderable, { name: 'cube', x: 3 });
            expect(entity.get(Mesh)).toBe(mesh);
            expect(mesh.name).toBe('cube');
            expect(entity.get(Renderable)).toEqual({ x: 3, y: 0, name: 'cube', visible: true });
        });

        it('add distributes initial values and only adds missing constituents', () => {
            const entity = world.spawn(Position({ x: 9, y: 9 }));
            entity.add(Movable({ x: 1, vx: 2 }));
            expect(entity.get(Position)).toEqual({ x: 9, y: 9 });
            expect(entity.get(Velocity)).toEqual({ vx: 2, vy: 0 });

            const other = world.spawn([Movable, { y: 3, vy: 4 }]);
            expect(other.get(Movable)).toEqual({ x: 0, y: 3, vx: 0, vy: 4 });
        });

        it('add supports tag and AoS constituents', () => {
            const Player = createAspect(Mesh, IsPlayer);
            const entity = world.spawn(Player({ name: 'hero' }));
            expect(entity.has(IsPlayer)).toBe(true);
            expect(entity.get(Mesh)).toEqual({ name: 'hero', visible: true });
        });

        it('remove removes all constituents', () => {
            const entity = world.spawn(Movable, Health);
            entity.remove(Movable);
            expect(entity.has(Position)).toBe(false);
            expect(entity.has(Velocity)).toBe(false);
            expect(entity.has(Health)).toBe(true);
        });

        it('works on the world entity', () => {
            world.add(Movable({ x: 1 }));
            expect(world.has(Movable)).toBe(true);
            world.set(Movable, { vx: 2 });
            expect(world.get(Movable)).toEqual({ x: 1, y: 0, vx: 2, vy: 0 });
            world.remove(Movable);
            expect(world.has(Movable)).toBe(false);
        });
    });

    describe('queries', () => {
        it('requires all constituents', () => {
            const a = world.spawn(Position, Velocity);
            world.spawn(Position);
            world.spawn(Velocity);

            expect([...world.query(Movable)]).toEqual([a]);
        });

        it('readEach delivers merged data', () => {
            world.spawn(Position({ x: 1, y: 2 }), Velocity({ vx: 3, vy: 4 }), Health({ hp: 5 }));

            const results: unknown[] = [];
            world.query(Health, Movable).readEach(([health, movable]) => {
                expectTypeOf(movable).toEqualTypeOf<{
                    x: number;
                    y: number;
                    vx: number;
                    vy: number;
                }>();
                results.push(health, movable);
            });

            expect(results).toEqual([{ hp: 5 }, { x: 1, y: 2, vx: 3, vy: 4 }]);
        });

        it('updateEach distributes writes back to constituent stores', () => {
            const entity = world.spawn(Movable({ vx: 1, vy: 2 }), Health);
            const positionChanged = vi.fn();
            const velocityChanged = vi.fn();
            world.onChange(Position, positionChanged);
            world.onChange(Velocity, velocityChanged);

            world.query(Movable, Health).updateEach(([movable, health]) => {
                movable.x += movable.vx;
                movable.y += movable.vy;
                health.hp -= 1;
            });

            expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
            expect(entity.get(Velocity)).toEqual({ vx: 1, vy: 2 });
            expect(entity.get(Health)).toEqual({ hp: 99 });
            expect(positionChanged).toHaveBeenCalledTimes(1);
            expect(velocityChanged).not.toHaveBeenCalled();
        });

        it('updateEach writes to AoS constituents', () => {
            const Renderable = createAspect(Position, Mesh);
            const entity = world.spawn(Renderable);
            const meshChanged = vi.fn();
            world.onChange(Mesh, meshChanged);

            world.query(Renderable).updateEach(([renderable]) => {
                renderable.visible = false;
            });

            expect(entity.get(Mesh)!.visible).toBe(false);
            expect(meshChanged).toHaveBeenCalledTimes(1);
        });

        it('skips aspects made only of tags in query data', () => {
            const IsEnemy = trait();
            const Tagged = createAspect(IsPlayer, IsEnemy);
            world.spawn(IsPlayer, IsEnemy, Health({ hp: 3 }));

            world.query(Tagged, Health).readEach((state) => {
                expect(state).toEqual([{ hp: 3 }]);
            });
        });

        it('Not with an aspect matches entities missing at least one constituent', () => {
            const both = world.spawn(Position, Velocity, Health);
            const onlyPosition = world.spawn(Position, Health);
            const none = world.spawn(Health);

            const result = world.query(Health, Not(Movable));
            expect(result).toContain(onlyPosition);
            expect(result).toContain(none);
            expect(result).not.toContain(both);

            both.remove(Velocity);
            expect(world.query(Health, Not(Movable))).toContain(both);

            onlyPosition.add(Velocity);
            expect(world.query(Health, Not(Movable))).not.toContain(onlyPosition);
        });

        it('Or with an aspect matches a complete aspect or any other trait', () => {
            const complete = world.spawn(Position, Velocity);
            const partial = world.spawn(Position);
            const health = world.spawn(Health);

            const result = world.query(Or(Movable, Health));
            expect(result).toContain(complete);
            expect(result).toContain(health);
            expect(result).not.toContain(partial);
        });

        it('Changed matches when any constituent changes', () => {
            const Changed = createChanged();
            const entity = world.spawn(Movable);
            const other = world.spawn(Movable);

            expect(world.query(Changed(Movable)).length).toBe(0);

            entity.set(Position, { x: 1 });
            other.set(Velocity, { vx: 1 });
            const result = world.query(Changed(Movable));
            expect(result).toContain(entity);
            expect(result).toContain(other);

            expect(world.query(Changed(Movable)).length).toBe(0);
        });

        it('Changed ignores entities missing a constituent', () => {
            const Changed = createChanged();
            const entity = world.spawn(Position);
            world.query(Changed(Movable));

            entity.set(Position, { x: 1 });
            expect(world.query(Changed(Movable)).length).toBe(0);
        });

        it('Changed tracks writes made through updateEach', () => {
            const Changed = createChanged();
            const entity = world.spawn(Movable);
            world.query(Changed(Movable));

            world.query(Movable).updateEach(
                ([movable]) => {
                    movable.vx = 10;
                },
                { changeDetection: 'always' }
            );

            expect([...world.query(Changed(Movable))]).toEqual([entity]);

            entity.set(Position, { x: 1 });

            let visited = 0;
            world.query(Changed(Movable)).updateEach(([movable]) => {
                visited++;
                movable.vy = 1;
            });

            expect(visited).toBe(1);
            expect(entity.get(Velocity)!.vy).toBe(1);
            expect([...world.query(Changed(Movable))]).toEqual([entity]);
        });

        it('Added matches the transition to all-present', () => {
            const Added = createAdded();
            world.query(Added(Movable));

            const entity = world.spawn(Position);
            expect(world.query(Added(Movable)).length).toBe(0);

            entity.add(Velocity);
            expect([...world.query(Added(Movable))]).toEqual([entity]);
            expect(world.query(Added(Movable)).length).toBe(0);

            entity.remove(Position);
            entity.add(Position);
            expect([...world.query(Added(Movable))]).toEqual([entity]);
        });

        it('Added includes entities that completed before the query was created', () => {
            const Added = createAdded();
            const entity = world.spawn(Movable);
            world.spawn(Position);

            expect([...world.query(Added(Movable))]).toEqual([entity]);
        });

        it('Removed matches the transition from all-present', () => {
            const Removed = createRemoved();
            world.query(Removed(Movable));

            const entity = world.spawn(Movable);
            const partial = world.spawn(Position);
            expect(world.query(Removed(Movable)).length).toBe(0);

            partial.remove(Position);
            expect(world.query(Removed(Movable)).length).toBe(0);

            entity.remove(Velocity);
            expect([...world.query(Removed(Movable))]).toEqual([entity]);

            entity.remove(Position);
            expect(world.query(Removed(Movable)).length).toBe(0);
        });

        it('Removed is cancelled when the aspect becomes complete again', () => {
            const Removed = createRemoved();
            world.query(Removed(Movable));

            const entity = world.spawn(Movable);
            entity.remove(Velocity);
            entity.add(Velocity);
            expect(world.query(Removed(Movable)).length).toBe(0);

            entity.remove(Velocity);
            expect([...world.query(Removed(Movable))]).toEqual([entity]);
        });

        it('distinguishes aspect queries from equivalent trait queries', () => {
            world.spawn(Position, Velocity);

            let aspectState: unknown[] = [];
            world.query(Movable).readEach((state) => (aspectState = [...state]));
            let traitState: unknown[] = [];
            world.query(Position, Velocity).readEach((state) => (traitState = [...state]));

            expect(aspectState).toEqual([{ x: 0, y: 0, vx: 0, vy: 0 }]);
            expect(traitState).toEqual([
                { x: 0, y: 0 },
                { vx: 0, vy: 0 },
            ]);
        });
    });

    describe('subscriptions', () => {
        it('onAdd fires when an entity becomes complete', () => {
            const cb = vi.fn();
            world.onAdd(Movable, cb);

            const entity = world.spawn(Position);
            expect(cb).not.toHaveBeenCalled();

            entity.add(Velocity);
            expect(cb).toHaveBeenCalledTimes(1);
            expect(cb).toHaveBeenCalledWith(entity);

            world.spawn(Movable);
            expect(cb).toHaveBeenCalledTimes(2);
        });

        it('onRemove fires when an entity stops being complete', () => {
            const cb = vi.fn();
            world.onRemove(Movable, cb);

            const entity = world.spawn(Movable);
            entity.remove(Velocity);
            expect(cb).toHaveBeenCalledTimes(1);
            expect(cb).toHaveBeenCalledWith(entity);

            entity.remove(Position);
            expect(cb).toHaveBeenCalledTimes(1);

            const other = world.spawn(Movable);
            other.remove(Movable);
            expect(cb).toHaveBeenCalledTimes(2);

            world.spawn(Movable).destroy();
            expect(cb).toHaveBeenCalledTimes(3);
        });

        it('onChange fires when any constituent changes while all are present', () => {
            const cb = vi.fn();
            const unsub = world.onChange(Movable, cb);

            const partial = world.spawn(Position);
            partial.set(Position, { x: 1 });
            expect(cb).not.toHaveBeenCalled();

            const entity = world.spawn(Movable);
            entity.set(Velocity, { vx: 1 });
            expect(cb).toHaveBeenCalledTimes(1);

            entity.set(Movable, { x: 1, vx: 2 });
            expect(cb).toHaveBeenCalledTimes(2);

            world.query(Movable).updateEach(([movable]) => {
                movable.x = 5;
                movable.vy = 5;
            });
            expect(cb).toHaveBeenCalledTimes(3);

            unsub();
            entity.set(Position, { x: 0 });
            expect(cb).toHaveBeenCalledTimes(3);
        });
    });
});
