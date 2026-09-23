import { beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest';
import {
    createAdded,
    createAspect,
    createChanged,
    createQuery,
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
const Mass = trait({ m: 1 });
const IsActive = trait();
const Transform = trait(() => ({ rotation: 0, scale: 1 }));

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    describe('creation', () => {
        it('exposes id, traits and a merged schema', () => {
            const Movement = createAspect(Position, Velocity);

            expect(typeof Movement.id).toBe('number');
            expect(Movement.traits).toEqual([Position, Velocity]);
            expect(Movement.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
        });

        it('returns a distinct instance for each call', () => {
            const A = createAspect(Position, Velocity);
            const B = createAspect(Position, Velocity);

            expect(A).not.toBe(B);
            expect(A.id).not.toBe(B.id);
            expect(A.id).not.toBe(Position.id);
            expect(A.id).not.toBe(Velocity.id);
        });

        it('throws on overlapping field names', () => {
            const Other = trait({ x: 5 });
            expect(() => createAspect(Position, Other)).toThrow();
        });

        it('throws on relation constituents', () => {
            const ChildOf = relation();
            // @ts-expect-error - relations are not valid constituents
            expect(() => createAspect(Position, ChildOf)).toThrow();
            // @ts-expect-error - relation pairs are not valid constituents
            expect(() => createAspect(Position, ChildOf('*'))).toThrow();
            expect(() => createAspect(Position, ordered(ChildOf))).toThrow();
        });

        it('accepts tag constituents', () => {
            const ActiveBody = createAspect(Position, IsActive);
            expect(ActiveBody.traits).toEqual([Position, IsActive]);
            expect(ActiveBody.schema).toEqual({ x: 0, y: 0 });
        });

        it('flattens nested aspects', () => {
            const Movement = createAspect(Position, Velocity);
            const Body = createAspect(Movement, Mass);

            expect(Body.traits).toEqual([Position, Velocity, Mass]);
            expect(Body.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0, m: 1 });
        });

        it('requires at least two traits', () => {
            expect(() => createAspect()).toThrow();
            expect(() => createAspect(Position)).toThrow();
        });
    });

    describe('entity operations', () => {
        const Movement = createAspect(Position, Velocity);

        it('has returns true only when every constituent is present', () => {
            const entity = world.spawn(Position);
            expect(entity.has(Movement)).toBe(false);

            entity.add(Velocity);
            expect(entity.has(Movement)).toBe(true);

            entity.remove(Position);
            expect(entity.has(Movement)).toBe(false);
        });

        it('get returns a merged object or undefined', () => {
            const entity = world.spawn(Position({ x: 1, y: 2 }));
            expect(entity.get(Movement)).toBeUndefined();

            entity.add(Velocity({ vx: 3, vy: 4 }));
            const data = entity.get(Movement);
            expect(data).toEqual({ x: 1, y: 2, vx: 3, vy: 4 });

            expectTypeOf(data).toEqualTypeOf<
                { x: number; y: number; vx: number; vy: number } | undefined
            >();
        });

        it('set distributes fields to constituents', () => {
            const entity = world.spawn(Movement);

            entity.set(Movement, { x: 1, vy: 2 });
            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
            expect(entity.get(Velocity)).toEqual({ vx: 0, vy: 2 });

            entity.set(Movement, (prev) => ({ x: prev.x + prev.vy }));
            expect(entity.get(Position)!.x).toBe(3);
        });

        it('set triggers change detection only for constituents that received fields', () => {
            const positionChanged = vi.fn();
            const velocityChanged = vi.fn();
            world.onChange(Position, positionChanged);
            world.onChange(Velocity, velocityChanged);

            const entity = world.spawn(Movement);
            entity.set(Movement, { x: 1 });

            expect(positionChanged).toHaveBeenCalledTimes(1);
            expect(velocityChanged).toHaveBeenCalledTimes(0);

            const Changed = createChanged();
            world.query(Changed(Velocity));
            entity.set(Movement, { vx: 1 });
            expect(world.query(Changed(Velocity))).toContain(entity);
            expect(velocityChanged).toHaveBeenCalledTimes(1);
        });

        it('add only adds missing constituents and distributes initial values', () => {
            const entity = world.spawn(Position({ x: 5, y: 5 }));

            entity.add(Movement({ x: 1, vx: 2 }));

            expect(entity.has(Movement)).toBe(true);
            expect(entity.get(Position)).toEqual({ x: 5, y: 5 });
            expect(entity.get(Velocity)).toEqual({ vx: 2, vy: 0 });
        });

        it('spawn accepts aspects', () => {
            const entity = world.spawn(Movement({ y: 3, vy: 4 }), IsActive);
            expect(entity.get(Movement)).toEqual({ x: 0, y: 3, vx: 0, vy: 4 });
            expect(entity.has(IsActive)).toBe(true);
        });

        it('remove removes all constituents', () => {
            const entity = world.spawn(Movement, Mass);
            entity.remove(Movement);

            expect(entity.has(Position)).toBe(false);
            expect(entity.has(Velocity)).toBe(false);
            expect(entity.has(Mass)).toBe(true);
        });

        it('supports tag constituents', () => {
            const ActiveBody = createAspect(Position, IsActive);
            const entity = world.spawn(ActiveBody({ x: 2 }));

            expect(entity.has(IsActive)).toBe(true);
            expect(entity.get(ActiveBody)).toEqual({ x: 2, y: 0 });

            entity.remove(IsActive);
            expect(entity.get(ActiveBody)).toBeUndefined();
        });

        it('supports AoS constituents', () => {
            const Body = createAspect(Position, Transform);
            const entity = world.spawn(Body({ rotation: 1 }));

            expect(entity.get(Body)).toEqual({ x: 0, y: 0, rotation: 1, scale: 1 });

            const instance = entity.get(Transform);
            entity.set(Body, { scale: 2 });
            expect(entity.get(Transform)).toBe(instance);
            expect(entity.get(Transform)).toEqual({ rotation: 1, scale: 2 });
        });

        it('works on the world entity', () => {
            world.add(Movement({ x: 1 }));
            expect(world.has(Movement)).toBe(true);
            expect(world.get(Movement)).toEqual({ x: 1, y: 0, vx: 0, vy: 0 });

            world.set(Movement, { vy: 9 });
            expect(world.get(Velocity)!.vy).toBe(9);

            world.remove(Movement);
            expect(world.has(Movement)).toBe(false);
        });
    });

    describe('queries', () => {
        const Movement = createAspect(Position, Velocity);

        it('requires all constituents', () => {
            const a = world.spawn(Position, Velocity);
            world.spawn(Position);
            world.spawn(Velocity);

            expect([...world.query(Movement)]).toEqual([a]);
            expect([...world.query(createQuery(Movement))]).toEqual([a]);
        });

        it('readEach delivers a merged data object', () => {
            world.spawn(Position({ x: 1 }), Velocity({ vx: 2 }), Mass({ m: 3 }));

            const seen: unknown[] = [];
            world.query(Movement, Mass).readEach(([movement, mass]) => {
                expectTypeOf(movement).toEqualTypeOf<{
                    x: number;
                    y: number;
                    vx: number;
                    vy: number;
                }>();
                seen.push({ ...movement }, { ...mass });
            });

            expect(seen).toEqual([{ x: 1, y: 0, vx: 2, vy: 0 }, { m: 3 }]);
        });

        it('updateEach distributes writes to constituent stores', () => {
            const entity = world.spawn(Movement({ vx: 1, vy: 2 }), Mass({ m: 2 }));

            world.query(Movement, Mass).updateEach(([movement, mass]) => {
                movement.x += movement.vx;
                movement.y += movement.vy;
                mass.m *= 2;
            });

            expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
            expect(entity.get(Mass)).toEqual({ m: 4 });
        });

        it('updateEach triggers per-trait change detection', () => {
            const Changed = createChanged();
            const entity = world.spawn(Movement({ vx: 1 }));
            world.query(Changed(Position));
            world.query(Changed(Velocity));

            // Position is tracked because it has a change subscriber.
            world.onChange(Position, () => {});
            world.query(Movement).updateEach(([movement]) => {
                movement.x += movement.vx;
            });
            expect([...world.query(Changed(Velocity))]).toEqual([]);

            world.query(Movement).updateEach(
                ([movement]) => {
                    movement.vy = 3;
                },
                { changeDetection: 'always' }
            );

            expect(world.query(Changed(Position))).toContain(entity);
            expect(world.query(Changed(Velocity))).toContain(entity);
            expect(entity.get(Velocity)!.vy).toBe(3);

            world.query(Movement).updateEach(
                ([movement]) => {
                    movement.vx = 10;
                },
                { changeDetection: 'never' }
            );
            expect(entity.get(Velocity)!.vx).toBe(10);
            expect(world.query(Changed(Velocity))).not.toContain(entity);
        });

        it('updateEach writes AoS constituent fields', () => {
            const Body = createAspect(Position, Transform);
            const entity = world.spawn(Body);

            world.query(Body).updateEach(([body]) => {
                body.rotation = 5;
                body.x = 1;
            });

            expect(entity.get(Transform)!.rotation).toBe(5);
            expect(entity.get(Position)!.x).toBe(1);
        });

        it('Not matches entities missing at least one constituent', () => {
            const both = world.spawn(Position, Velocity);
            const onlyPosition = world.spawn(Position);
            const none = world.spawn();

            let entities = world.query(Not(Movement));
            expect(entities).toContain(onlyPosition);
            expect(entities).toContain(none);
            expect(entities).not.toContain(both);

            both.remove(Velocity);
            onlyPosition.add(Velocity);

            entities = world.query(Not(Movement));
            expect(entities).toContain(both);
            expect(entities).not.toContain(onlyPosition);

            const created = world.spawn(Velocity);
            expect(world.query(Not(Movement))).toContain(created);
            expect([...world.query(Mass, Not(Movement))]).toEqual([]);
        });

        it('Not with an aspect differs from Not with its traits', () => {
            const onlyPosition = world.spawn(Position);

            expect(world.query(Not(Movement))).toContain(onlyPosition);
            expect(world.query(Not(Position, Velocity))).not.toContain(onlyPosition);
        });

        it('Or matches when any aspect is complete or any trait is present', () => {
            const complete = world.spawn(Position, Velocity);
            const massOnly = world.spawn(Mass);
            const partial = world.spawn(Position);

            const entities = world.query(Or(Movement, Mass));
            expect(entities).toContain(complete);
            expect(entities).toContain(massOnly);
            expect(entities).not.toContain(partial);
        });

        it('Changed matches when any constituent changed', () => {
            const Changed = createChanged();
            const a = world.spawn(Movement);
            const b = world.spawn(Movement);
            const partial = world.spawn(Position);

            expect([...world.query(Changed(Movement))]).toEqual([]);

            a.set(Position, { x: 1 });
            b.set(Velocity, { vx: 1 });
            partial.set(Position, { x: 1 });

            const entities = world.query(Changed(Movement));
            expect(entities).toContain(a);
            expect(entities).toContain(b);
            expect(entities).not.toContain(partial);

            expect([...world.query(Changed(Movement))]).toEqual([]);

            // A change before the aspect is complete does not count.
            partial.set(Position, { x: 2 });
            partial.add(Velocity);
            expect([...world.query(Changed(Movement))]).toEqual([]);
        });

        it('Changed with an aspect is tracked by updateEach', () => {
            const Changed = createChanged();
            const entity = world.spawn(Movement);
            world.query(Changed(Movement));
            entity.set(Position, { x: 1 });

            // Constituents of Changed(aspect) are tracked in auto mode.
            let visited = 0;
            world.query(Changed(Movement)).updateEach(([movement]) => {
                visited++;
                movement.vx = 1;
            });

            expect(visited).toBe(1);
            expect([...world.query(Changed(Movement))]).toEqual([entity]);
        });

        it('Added matches the transition to all-present', () => {
            const Added = createAdded();
            world.query(Added(Movement));

            const entity = world.spawn(Position);
            expect([...world.query(Added(Movement))]).toEqual([]);

            entity.add(Velocity);
            expect([...world.query(Added(Movement))]).toEqual([entity]);
            expect([...world.query(Added(Movement))]).toEqual([]);

            entity.remove(Position);
            expect([...world.query(Added(Movement))]).toEqual([]);

            entity.add(Position);
            expect([...world.query(Added(Movement))]).toEqual([entity]);

            const spawned = world.spawn(Movement);
            expect([...world.query(Added(Movement))]).toEqual([spawned]);
        });

        it('Removed matches the transition from all-present', () => {
            const Removed = createRemoved();
            world.query(Removed(Movement));

            const entity = world.spawn(Movement);
            const partial = world.spawn(Position);
            expect([...world.query(Removed(Movement))]).toEqual([]);

            partial.remove(Position);
            expect([...world.query(Removed(Movement))]).toEqual([]);

            entity.remove(Velocity);
            expect([...world.query(Removed(Movement))]).toEqual([entity]);
            expect([...world.query(Removed(Movement))]).toEqual([]);

            entity.remove(Position);
            expect([...world.query(Removed(Movement))]).toEqual([]);

            // Removing and restoring within a frame is not a removal.
            entity.add(Position, Velocity);
            entity.remove(Velocity);
            entity.add(Velocity);
            expect([...world.query(Removed(Movement))]).toEqual([]);

            entity.destroy();
            expect([...world.query(Removed(Movement))]).toEqual([entity]);
        });

        it('works when constituents span trait generations', () => {
            const Added = createAdded();
            const Removed = createRemoved();
            const fillers = Array.from({ length: 40 }, () => trait());
            const Late = trait({ late: 0 });
            const Spanning = createAspect(Position, Late);

            world.spawn(...fillers);
            world.query(Added(Spanning));
            world.query(Removed(Spanning));

            const entity = world.spawn(Position);
            expect([...world.query(Not(Spanning))]).toContain(entity);

            entity.add(Late({ late: 2 }));
            expect([...world.query(Spanning)]).toEqual([entity]);
            expect([...world.query(Not(Spanning))]).not.toContain(entity);
            expect([...world.query(Added(Spanning))]).toEqual([entity]);
            expect(entity.get(Spanning)).toEqual({ x: 0, y: 0, late: 2 });

            entity.remove(Position);
            expect([...world.query(Removed(Spanning))]).toEqual([entity]);
        });

        it('Changed with an aspect composes inside Or', () => {
            const Changed = createChanged();
            const a = world.spawn(Movement);
            const b = world.spawn(Mass);
            world.query(Or(Changed(Movement), Changed(Mass)));

            a.set(Velocity, { vx: 1 });
            b.set(Mass, { m: 2 });

            const entities = world.query(Or(Changed(Movement), Changed(Mass)));
            expect(entities).toHaveLength(2);
            expect(entities).toContain(a);
            expect(entities).toContain(b);
        });

        it('composes with other parameters and modifiers', () => {
            const Added = createAdded();
            const a = world.spawn(Movement, Mass);
            const b = world.spawn(Movement, Mass, IsActive);
            world.spawn(Position, Mass);

            expect([...world.query(Movement, Mass, Not(IsActive))]).toEqual([a]);
            expect(world.query(Mass, Not(Movement))).toHaveLength(1);

            world.query(Added(Movement), IsActive);
            const c = world.spawn(Position, IsActive);
            c.add(Velocity);
            expect([...world.query(Added(Movement), IsActive)]).toEqual([c]);

            const Removed = createRemoved();
            world.query(Or(Removed(Movement), Removed(Mass)));
            b.remove(Position);
            a.remove(Mass);
            const removed = world.query(Or(Removed(Movement), Removed(Mass)));
            expect(removed).toContain(a);
            expect(removed).toContain(b);
        });
    });

    describe('hooks', () => {
        const Movement = createAspect(Position, Velocity);

        it('onAdd fires when an entity becomes complete', () => {
            const cb = vi.fn();
            world.onAdd(Movement, cb);

            const entity = world.spawn(Position);
            expect(cb).not.toHaveBeenCalled();

            entity.add(Velocity);
            expect(cb).toHaveBeenCalledTimes(1);
            expect(cb).toHaveBeenCalledWith(entity);

            world.spawn(Movement);
            expect(cb).toHaveBeenCalledTimes(2);
        });

        it('onAdd reads initialized values', () => {
            let data: unknown;
            world.onAdd(Movement, (e) => (data = e.get(Movement)));
            world.spawn(Movement({ x: 1, vx: 2 }));
            expect(data).toEqual({ x: 1, y: 0, vx: 2, vy: 0 });
        });

        it('onRemove fires when an entity stops being complete', () => {
            const cb = vi.fn();
            world.onRemove(Movement, cb);

            const entity = world.spawn(Movement);
            entity.remove(Position);
            expect(cb).toHaveBeenCalledTimes(1);

            entity.remove(Velocity);
            expect(cb).toHaveBeenCalledTimes(1);

            const other = world.spawn(Movement);
            other.remove(Movement);
            expect(cb).toHaveBeenCalledTimes(2);

            world.spawn(Movement).destroy();
            expect(cb).toHaveBeenCalledTimes(3);
        });

        it('onChange fires when a constituent changes while complete', () => {
            const cb = vi.fn();
            world.onChange(Movement, cb);

            const partial = world.spawn(Position);
            partial.set(Position, { x: 1 });
            expect(cb).not.toHaveBeenCalled();

            const entity = world.spawn(Movement);
            entity.set(Velocity, { vx: 1 });
            expect(cb).toHaveBeenCalledTimes(1);

            entity.set(Movement, { x: 1, vx: 2 });
            expect(cb).toHaveBeenCalledTimes(2);

            world.query(Movement).updateEach(([movement]) => {
                movement.x = 5;
                movement.vy = 5;
            });
            expect(cb).toHaveBeenCalledTimes(3);
        });

        it('unsubscribes from every constituent', () => {
            const cb = vi.fn();
            const unsub = world.onAdd(Movement, cb);
            unsub();

            world.spawn(Movement);
            expect(cb).not.toHaveBeenCalled();
        });
    });
});
