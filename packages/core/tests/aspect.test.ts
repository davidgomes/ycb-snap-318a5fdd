import { beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest';
import {
    type Aspect,
    type AspectRecord,
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
const Mass = trait({ mass: 1 });
const IsActive = trait();
const IsVisible = trait();

const Body = createAspect(Position, Velocity);

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    describe('createAspect', () => {
        it('should expose id, traits and a merged schema', () => {
            expect(typeof Body.id).toBe('number');
            expect(Body.traits).toEqual([Position, Velocity]);
            expect(Body.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
        });

        it('should return a distinct instance for each call', () => {
            const A = createAspect(Position, Velocity);
            const B = createAspect(Position, Velocity);

            expect(A).not.toBe(B);
            expect(A.id).not.toBe(B.id);
            expect(A.id).not.toBe(Body.id);
        });

        it('should require at least two traits', () => {
            expect(() => createAspect()).toThrow();
            expect(() => createAspect(Position)).toThrow();
            expect(() => createAspect(Position, Position)).toThrow();
        });

        it('should throw on overlapping field names', () => {
            const OtherPosition = trait({ x: 0, z: 0 });
            expect(() => createAspect(Position, OtherPosition)).toThrow(/"x"/);
            expect(() => createAspect(Body, trait({ vx: 0 }))).toThrow(/"vx"/);
        });

        it('should throw on relation constituents', () => {
            const ChildOf = relation();
            const Contains = relation({ store: { amount: 0 } });

            // @ts-expect-error - testing the error case
            expect(() => createAspect(Position, ChildOf)).toThrow();
            // @ts-expect-error - testing the error case
            expect(() => createAspect(Position, Contains)).toThrow();
            // @ts-expect-error - testing the error case
            expect(() => createAspect(Position, ChildOf('*'))).toThrow();
            expect(() => createAspect(Position, ordered(ChildOf))).toThrow();
        });

        it('should accept tag traits', () => {
            const ActiveBody = createAspect(Position, IsActive);

            expect(ActiveBody.traits).toEqual([Position, IsActive]);
            expect(ActiveBody.schema).toEqual({ x: 0, y: 0 });
            expect(() => createAspect(IsActive, IsVisible)).not.toThrow();
        });

        it('should flatten nested aspects to their traits', () => {
            const Physics = createAspect(Body, Mass);
            expect(Physics.traits).toEqual([Position, Velocity, Mass]);
            expect(Physics.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0, mass: 1 });

            // Shared traits are only included once.
            const Overlap = createAspect(Body, createAspect(Velocity, Mass));
            expect(Overlap.traits).toEqual([Position, Velocity, Mass]);
        });

        it('should use the fields of callback-based traits', () => {
            const Transform = trait(() => ({ rotation: 0, scale: 1 }));
            const Renderable = createAspect(Position, Transform);

            expect(Renderable.schema).toEqual({ x: 0, y: 0, rotation: 0, scale: 1 });
            expect(() =>
                createAspect(
                    Position,
                    trait(() => ({ x: 1 }))
                )
            ).toThrow(/"x"/);
        });
    });

    describe('entity operations', () => {
        it('has should require every constituent', () => {
            const entity = world.spawn(Position);
            expect(entity.has(Body)).toBe(false);

            entity.add(Velocity);
            expect(entity.has(Body)).toBe(true);

            entity.remove(Position);
            expect(entity.has(Body)).toBe(false);
        });

        it('get should merge constituent fields or return undefined', () => {
            const entity = world.spawn(Position({ x: 1, y: 2 }));
            expect(entity.get(Body)).toBeUndefined();

            entity.add(Velocity({ vx: 3, vy: 4 }));
            expect(entity.get(Body)).toEqual({ x: 1, y: 2, vx: 3, vy: 4 });

            // Tag constituents don't contribute fields.
            const ActiveBody = createAspect(Body, IsActive);
            expect(entity.get(ActiveBody)).toBeUndefined();
            entity.add(IsActive);
            expect(entity.get(ActiveBody)).toEqual({ x: 1, y: 2, vx: 3, vy: 4 });
        });

        it('set should distribute fields and only flag the owning traits as changed', () => {
            const Changed = createChanged();
            const entity = world.spawn(Position, Velocity);

            world.query(Changed(Position));
            world.query(Changed(Velocity));

            entity.set(Body, { x: 10 });

            expect(entity.get(Position)).toEqual({ x: 10, y: 0 });
            expect(entity.get(Velocity)).toEqual({ vx: 0, vy: 0 });
            expect([...world.query(Changed(Position))]).toEqual([entity]);
            expect(world.query(Changed(Velocity))).toHaveLength(0);

            entity.set(Body, { y: 5, vx: 2 });
            expect(entity.get(Body)).toEqual({ x: 10, y: 5, vx: 2, vy: 0 });
            expect([...world.query(Changed(Position))]).toEqual([entity]);
            expect([...world.query(Changed(Velocity))]).toEqual([entity]);
        });

        it('set should accept a callback with the previous merged value', () => {
            const entity = world.spawn(Position({ x: 1, y: 1 }), Velocity({ vx: 2, vy: 3 }));

            entity.set(Body, (prev) => ({ x: prev.x + prev.vx, y: prev.y + prev.vy }));

            expect(entity.get(Body)).toEqual({ x: 3, y: 4, vx: 2, vy: 3 });
        });

        it('set should not flag changes when told not to', () => {
            const Changed = createChanged();
            const entity = world.spawn(Position, Velocity);
            world.query(Changed(Position));

            entity.set(Body, { x: 5 }, false);

            expect(entity.get(Position)!.x).toBe(5);
            expect(world.query(Changed(Position))).toHaveLength(0);
        });

        it('add should only add missing constituents and distribute initial values', () => {
            const entity = world.spawn(Position({ x: 1, y: 1 }));

            entity.add(Body({ x: 100, vx: 5 }));

            // Position already existed so its values are untouched.
            expect(entity.get(Body)).toEqual({ x: 1, y: 1, vx: 5, vy: 0 });

            const addPosition = vi.fn();
            world.onAdd(Position, addPosition);
            entity.add(Body({ x: 50 }));

            expect(addPosition).not.toHaveBeenCalled();
            expect(entity.get(Position)!.x).toBe(1);
        });

        it('should spawn with aspects', () => {
            const entityA = world.spawn(Body);
            const entityB = world.spawn(Body({ y: 7, vy: 8 }), IsActive);

            expect(entityA.get(Body)).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
            expect(entityB.get(Body)).toEqual({ x: 0, y: 7, vx: 0, vy: 8 });
            expect(entityB.has(IsActive)).toBe(true);
        });

        it('remove should remove every constituent', () => {
            const entity = world.spawn(Position, Velocity, Mass);

            entity.remove(Body);

            expect(entity.has(Position)).toBe(false);
            expect(entity.has(Velocity)).toBe(false);
            expect(entity.has(Mass)).toBe(true);

            // Removing an incomplete aspect removes whatever constituents remain.
            entity.add(Velocity);
            entity.remove(Body);
            expect(entity.has(Velocity)).toBe(false);
        });

        it('changed should flag every constituent', () => {
            const Changed = createChanged();
            const entity = world.spawn(Position, Velocity);
            world.query(Changed(Position, Velocity));

            entity.changed(Body);

            expect([...world.query(Changed(Position, Velocity))]).toEqual([entity]);
        });

        it('should work with callback-based traits', () => {
            const Transform = trait(() => ({ rotation: 0, scale: 1 }));
            const Renderable = createAspect(Position, Transform);

            const entity = world.spawn(Renderable({ x: 1, rotation: 2 }));
            const transform = entity.get(Transform)!;

            expect(transform).toEqual({ rotation: 2, scale: 1 });
            expect(entity.get(Renderable)).toEqual({ x: 1, y: 0, rotation: 2, scale: 1 });

            // Writes mutate the stored instance rather than replacing it.
            entity.set(Renderable, { scale: 3, y: 4 });
            expect(entity.get(Transform)).toBe(transform);
            expect(transform.scale).toBe(3);
            expect(entity.get(Position)!.y).toBe(4);
        });

        it('should work with world traits', () => {
            const Time = trait({ delta: 0 });
            const Clock = trait({ elapsed: 0 });
            const Timing = createAspect(Time, Clock);

            world.add(Timing({ delta: 1 }));
            expect(world.has(Timing)).toBe(true);
            expect(world.get(Timing)).toEqual({ delta: 1, elapsed: 0 });

            world.set(Timing, (prev) => ({ elapsed: prev.elapsed + prev.delta }));
            expect(world.get(Clock)!.elapsed).toBe(1);

            world.remove(Timing);
            expect(world.has(Time)).toBe(false);
            expect(world.has(Clock)).toBe(false);
        });
    });

    describe('queries', () => {
        it('should require every constituent', () => {
            const entityA = world.spawn(Position, Velocity);
            world.spawn(Position);
            world.spawn(Velocity);
            const entityD = world.spawn(Position, Velocity, Mass);

            expect([...world.query(Body)]).toEqual([entityA, entityD]);
            expect([...world.query(Body, Mass)]).toEqual([entityD]);
        });

        it('should stay in sync with structural changes', () => {
            const entity = world.spawn(Position);
            expect(world.query(Body)).toHaveLength(0);

            entity.add(Velocity);
            expect([...world.query(Body)]).toEqual([entity]);

            entity.remove(Position);
            expect(world.query(Body)).toHaveLength(0);
        });

        it('readEach should deliver a merged data object', () => {
            world.spawn(Position({ x: 1, y: 2 }), Velocity({ vx: 3, vy: 4 }), Mass({ mass: 5 }));

            const results: unknown[] = [];
            world.query(Mass, Body).readEach(([mass, body]) => {
                results.push({ ...mass }, { ...body });
            });

            expect(results).toEqual([{ mass: 5 }, { x: 1, y: 2, vx: 3, vy: 4 }]);
        });

        it('updateEach should distribute writes back to constituent stores', () => {
            const entity = world.spawn(Position({ x: 1, y: 1 }), Velocity({ vx: 2, vy: 3 }));

            world.query(Body).updateEach(([body]) => {
                body.x += body.vx;
                body.y += body.vy;
                body.vx = 0;
            });

            expect(entity.get(Position)).toEqual({ x: 3, y: 4 });
            expect(entity.get(Velocity)).toEqual({ vx: 0, vy: 3 });
        });

        it('updateEach should detect changes per trait', () => {
            const Changed = createChanged();
            const entity = world.spawn(Position, Velocity);

            world.query(Changed(Position));
            world.query(Changed(Velocity));
            const onPosition = vi.fn();
            const onVelocity = vi.fn();
            world.onChange(Position, onPosition);
            world.onChange(Velocity, onVelocity);

            world.query(Body).updateEach(([body]) => {
                body.x = 10;
            });

            expect(onPosition).toHaveBeenCalledTimes(1);
            expect(onVelocity).not.toHaveBeenCalled();
            expect([...world.query(Changed(Position))]).toEqual([entity]);
            expect(world.query(Changed(Velocity))).toHaveLength(0);

            // Writing the same value is not a change.
            world.query(Body).updateEach(([body]) => {
                body.x = 10;
            });
            expect(onPosition).toHaveBeenCalledTimes(1);
        });

        it('updateEach should write callback-based trait fields to the stored instance', () => {
            const Transform = trait(() => ({ rotation: 0, scale: 1 }));
            const Renderable = createAspect(Position, Transform);
            const entity = world.spawn(Renderable);
            const instance = entity.get(Transform)!;
            const onTransform = vi.fn();
            world.onChange(Transform, onTransform);

            world.query(Renderable).updateEach(([renderable]) => {
                renderable.rotation = 90;
            });

            expect(entity.get(Transform)).toBe(instance);
            expect(instance.rotation).toBe(90);
            expect(onTransform).toHaveBeenCalledTimes(1);
        });

        it('should exclude tag-only aspects and tag fields from query data', () => {
            const Flags = createAspect(IsActive, IsVisible);
            const entity = world.spawn(Position({ x: 1 }), Velocity, IsActive, IsVisible);

            world.query(Flags, createAspect(Position, IsActive)).readEach((state, e) => {
                expect(e).toBe(entity);
                expect(state).toEqual([{ x: 1, y: 0 }]);
            });
        });

        it('should support select and useStores', () => {
            const entity = world.spawn(Position({ x: 1 }), Velocity({ vx: 2 }), Mass);

            world
                .query(Position, Velocity, Mass)
                .select(Body)
                .updateEach(([body]) => {
                    body.x = body.vx * 10;
                });
            expect(entity.get(Position)!.x).toBe(20);

            world.query(Mass, Body).useStores(([mass, position, velocity], entities) => {
                expect([...entities]).toEqual([entity]);
                expect(mass.mass[entity.id()]).toBe(1);
                expect(position.x[entity.id()]).toBe(20);
                expect(velocity.vx[entity.id()]).toBe(2);
            });
        });

        it('should not share results with the equivalent trait query', () => {
            world.spawn(Position({ x: 1 }), Velocity({ vx: 2 }));

            const byTraits = createQuery(Position, Velocity);
            const byAspect = createQuery(Body);
            expect(byTraits).not.toBe(byAspect);

            world.query(byAspect).readEach((state) => {
                expect(state).toEqual([{ x: 1, y: 0, vx: 2, vy: 0 }]);
            });
            world.query(byTraits).readEach((state) => {
                expect(state).toEqual([
                    { x: 1, y: 0 },
                    { vx: 2, vy: 0 },
                ]);
            });
        });

        it('Not should match entities missing at least one constituent', () => {
            const entityA = world.spawn(Position, Velocity);
            const entityB = world.spawn(Position);
            const entityC = world.spawn(Velocity);
            const entityD = world.spawn(Mass);

            expect([...world.query(Not(Body))]).toEqual([entityB, entityC, entityD]);
            expect([...world.query(Position, Not(Body))]).toEqual([entityB]);
            expect([...world.query(Not(Body, Mass))]).toEqual([entityB, entityC]);

            entityA.remove(Velocity);
            const entityE = world.spawn();
            entityB.add(Velocity);

            expect([...world.query(Position, Not(Body))]).toEqual([entityA]);
            expect(world.query(Not(Body))).toContain(entityE);
            expect(world.query(Not(Body))).not.toContain(entityB);
        });

        it('Or should match entities with any trait or a complete aspect', () => {
            const entityA = world.spawn(Position, Velocity);
            world.spawn(Position);
            const entityC = world.spawn(Mass);
            const entityD = world.spawn(Velocity, IsActive);

            expect([...world.query(Or(Body, Mass))]).toEqual([entityA, entityC]);
            expect([...world.query(Or(Body, Mass), Not(IsActive))]).toEqual([entityA, entityC]);

            entityD.add(Position);
            expect([...world.query(Or(Body, Mass))]).toEqual([entityA, entityC, entityD]);
        });

        it('Or should read aspects safely for entities matched by another trait', () => {
            const Transform = trait(() => ({ rotation: 0 }));
            const Renderable = createAspect(Position, Transform);
            const entity = world.spawn(Mass({ mass: 2 }));

            world.query(Or(Renderable, Mass)).updateEach(([renderable, mass], e) => {
                expect(e).toBe(entity);
                expect(renderable.rotation).toBeUndefined();
                expect(mass.mass).toBe(2);
            });
        });

        it('Changed should match when any constituent data changed', () => {
            const Changed = createChanged();
            const entityA = world.spawn(Position, Velocity);
            const entityB = world.spawn(Position, Velocity);
            const entityC = world.spawn(Position);

            expect(world.query(Changed(Body))).toHaveLength(0);

            entityA.set(Position, { x: 1 });
            entityB.set(Velocity, { vx: 1 });
            entityC.set(Position, { x: 1 });

            expect([...world.query(Changed(Body))]).toEqual([entityA, entityB]);
            expect(world.query(Changed(Body))).toHaveLength(0);

            // Changes while the aspect is incomplete don't count once it completes.
            entityC.add(Velocity);
            expect(world.query(Changed(Body))).toHaveLength(0);

            // Removing a constituent invalidates the change.
            entityA.set(Position, { x: 2 });
            entityA.remove(Velocity);
            expect(world.query(Changed(Body))).toHaveLength(0);
        });

        it('Changed should combine with other modifiers', () => {
            const Changed = createChanged();
            const entityA = world.spawn(Position, Velocity, Mass);
            const entityB = world.spawn(Position, Velocity, Mass, IsActive);

            world.query(Changed(Body), Not(IsActive));
            world.query(Changed(Body, Mass));

            entityA.set(Body, { x: 1 });
            entityB.set(Body, { x: 1 });
            expect([...world.query(Changed(Body), Not(IsActive))]).toEqual([entityA]);

            // Mixing an aspect and a trait in one modifier requires both to have changed.
            expect(world.query(Changed(Body, Mass))).toHaveLength(0);
            entityA.set(Mass, { mass: 2 });
            expect([...world.query(Changed(Body, Mass))]).toEqual([entityA]);
        });

        it('Changed should populate when created after the changes', () => {
            const Changed = createChanged();
            const entityA = world.spawn(Position, Velocity);
            const entityB = world.spawn(Position);

            entityA.set(Velocity, { vx: 1 });
            entityB.set(Position, { x: 1 });

            expect([...world.query(Changed(Body))]).toEqual([entityA]);
        });

        it('Added should match the transition to all-present', () => {
            const Added = createAdded();
            const entityA = world.spawn(Position);
            const entityB = world.spawn(Position, Velocity);

            expect([...world.query(Added(Body))]).toEqual([entityB]);

            entityA.add(Velocity);
            world.spawn(Mass);
            expect([...world.query(Added(Body))]).toEqual([entityA]);
            expect(world.query(Added(Body))).toHaveLength(0);

            // Adding an unrelated trait is not a transition.
            entityA.add(Mass);
            expect(world.query(Added(Body))).toHaveLength(0);

            // Completing and then breaking the aspect before the query is not a match.
            entityA.remove(Position);
            entityA.add(Position);
            entityA.remove(Velocity);
            expect(world.query(Added(Body))).toHaveLength(0);

            // Leaving and returning to complete is a transition.
            entityA.add(Velocity);
            entityB.remove(Velocity);
            entityB.add(Velocity);
            expect([...world.query(Added(Body))]).toEqual([entityA, entityB]);
        });

        it('Removed should match the transition from all-present', () => {
            const Removed = createRemoved();
            const entityA = world.spawn(Position, Velocity);
            const entityB = world.spawn(Position);
            const entityC = world.spawn(Position, Velocity);

            expect(world.query(Removed(Body))).toHaveLength(0);

            entityA.remove(Velocity);
            entityB.remove(Position);
            world.spawn(Mass);
            expect([...world.query(Removed(Body))]).toEqual([entityA]);
            expect(world.query(Removed(Body))).toHaveLength(0);

            // Removing more constituents keeps the transition, completing again cancels it.
            entityC.remove(Position);
            entityC.remove(Velocity);
            entityC.add(Position);
            expect([...world.query(Removed(Body))]).toEqual([entityC]);

            entityA.add(Velocity);
            entityA.remove(Position);
            entityA.add(Position);
            expect(world.query(Removed(Body))).toHaveLength(0);

            // Destroyed entities that had the aspect are included.
            entityA.destroy();
            expect([...world.query(Removed(Body))]).toEqual([entityA]);
        });

        it('Removed should populate when created after the removals', () => {
            const Removed = createRemoved();
            const entityA = world.spawn(Position, Velocity);
            const entityB = world.spawn(Position);

            entityA.remove(Position);
            entityB.remove(Position);

            expect([...world.query(Removed(Body))]).toEqual([entityA]);
        });

        it('should compose tracking modifiers with Or', () => {
            const Added = createAdded();
            const entityA = world.spawn(Position);
            const entityB = world.spawn();

            world.query(Or(Added(Body), Added(Mass)));

            entityA.add(Velocity);
            entityB.add(Mass);

            expect([...world.query(Or(Added(Body), Added(Mass)))]).toEqual([entityA, entityB]);
        });
    });

    describe('events', () => {
        it('onAdd should fire when an entity transitions to complete', () => {
            const onAdd = vi.fn();
            world.onAdd(Body, onAdd);

            const entity = world.spawn(Position);
            expect(onAdd).not.toHaveBeenCalled();

            entity.add(Velocity({ vx: 5 }));
            expect(onAdd).toHaveBeenCalledTimes(1);
            expect(onAdd).toHaveBeenCalledWith(entity);

            // Adding both constituents at once fires once, after values are set.
            let body: AspectRecord<typeof Body> | undefined;
            world.onAdd(Body, (e) => (body = e.get(Body)));
            world.spawn(Body({ x: 1, vy: 2 }));
            expect(onAdd).toHaveBeenCalledTimes(2);
            expect(body).toEqual({ x: 1, y: 0, vx: 0, vy: 2 });

            entity.add(Mass);
            expect(onAdd).toHaveBeenCalledTimes(2);
        });

        it('onRemove should fire when an entity transitions from complete', () => {
            const onRemove = vi.fn();
            let body: AspectRecord<typeof Body> | undefined;
            world.onRemove(Body, (entity) => {
                onRemove(entity);
                body = entity.get(Body);
            });

            const entity = world.spawn(Position({ x: 3 }), Velocity);
            entity.remove(Velocity);
            expect(onRemove).toHaveBeenCalledTimes(1);
            expect(onRemove).toHaveBeenCalledWith(entity);
            // Fires before the data is removed.
            expect(body).toEqual({ x: 3, y: 0, vx: 0, vy: 0 });

            entity.remove(Position);
            expect(onRemove).toHaveBeenCalledTimes(1);

            const other = world.spawn(Body);
            other.remove(Body);
            expect(onRemove).toHaveBeenCalledTimes(2);

            const destroyed = world.spawn(Body);
            destroyed.destroy();
            expect(onRemove).toHaveBeenCalledTimes(3);
        });

        it('onChange should fire when any constituent changes while complete', () => {
            const onChange = vi.fn();
            const unsub = world.onChange(Body, onChange);

            const entity = world.spawn(Position);
            entity.set(Position, { x: 1 });
            expect(onChange).not.toHaveBeenCalled();

            entity.add(Velocity);
            entity.set(Position, { x: 2 });
            entity.set(Velocity, { vx: 2 });
            expect(onChange).toHaveBeenCalledTimes(2);
            expect(onChange).toHaveBeenCalledWith(entity);

            world.query(Body).updateEach(([body]) => {
                body.y = 5;
            });
            expect(onChange).toHaveBeenCalledTimes(3);

            unsub();
            entity.set(Position, { x: 3 });
            expect(onChange).toHaveBeenCalledTimes(3);
        });
    });

    it('should have typed records', () => {
        expectTypeOf(Body).toMatchTypeOf<Aspect>();
        expectTypeOf<AspectRecord<typeof Body>>().toEqualTypeOf<{
            x: number;
            y: number;
            vx: number;
            vy: number;
        }>();

        const entity = world.spawn(Body);
        expectTypeOf(entity.get(Body)).toEqualTypeOf<
            { x: number; y: number; vx: number; vy: number } | undefined
        >();

        const Physics = createAspect(Body, Mass, IsActive);
        expectTypeOf(Physics.traits).toEqualTypeOf<
            readonly [typeof Position, typeof Velocity, typeof Mass, typeof IsActive]
        >();

        world.query(Physics, Position).readEach(([physics, position]) => {
            expectTypeOf(physics).toEqualTypeOf<{
                x: number;
                y: number;
                vx: number;
                vy: number;
                mass: number;
            }>();
            expectTypeOf(position).toEqualTypeOf<{ x: number; y: number }>();
        });

        world.query(Not(Body), Mass).readEach((state) => {
            expectTypeOf(state).toEqualTypeOf<[{ mass: number }]>();
        });

        // @ts-expect-error - unknown fields are rejected
        Body({ z: 1 });
    });
});
