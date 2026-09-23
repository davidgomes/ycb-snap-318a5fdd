import { beforeEach, describe, expect, expectTypeOf, it } from 'vitest';
import {
    $internal,
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
const Health = trait(() => ({ hp: 10 }));
const IsActive = trait();
const IsDead = trait();

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('returns a distinct instance for every call', () => {
        const a = createAspect(Position, Velocity);
        const b = createAspect(Position, Velocity);

        expect(a).not.toBe(b);
        expect(a.id).not.toBe(b.id);
        expect(a.traits).toEqual([Position, Velocity]);
        expect(a.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
    });

    it('flattens nested aspects and rejects invalid constituents', () => {
        const inner = createAspect(Position, Velocity);
        const outer = createAspect(inner, IsActive);

        expect(outer.traits).toEqual([Position, Velocity, IsActive]);
        expect(outer.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });

        expect(() => createAspect(Position, Velocity, inner)).toThrow(/more than once/i);
        expect(() => createAspect(Position, trait({ x: 1 }))).toThrow(/"x"/);
        expect(() => (createAspect as (trait: typeof Position) => unknown)(Position)).toThrow(
            /at least two/i
        );
        expect(() => createAspect(Position, {} as never)).toThrow(/expected a trait or aspect/i);

        const ChildOf = relation();
        const parent = world.spawn();
        expect(() => createAspect(Position, ChildOf as never)).toThrow(/relation/i);
        expect(() => createAspect(Position, ChildOf(parent) as never)).toThrow(/relation/i);
        expect(() => createAspect(Position, ordered(ChildOf) as never)).toThrow(/relation/i);
        expect(() => createAspect(Position, ChildOf[$internal].trait as never)).toThrow(/relation/i);
    });

    it('treats tag constituents as flags with an empty merged record', () => {
        const Flags = createAspect(IsActive, IsDead);
        const entity = world.spawn(IsActive);

        expect(entity.has(Flags)).toBe(false);
        expect(entity.get(Flags)).toBeUndefined();

        entity.add(IsDead);
        expect(entity.has(Flags)).toBe(true);
        expect(entity.get(Flags)).toEqual({});

        entity.remove(IsActive);
        expect(entity.has(Flags)).toBe(false);
    });

    it('has, get, set, add, and remove operate on the whole group', () => {
        const Movement = createAspect(Position, Velocity);
        const entity = world.spawn();

        expect(entity.has(Movement)).toBe(false);
        expect(entity.get(Movement)).toBeUndefined();

        entity.add(Position({ x: 5, y: 6 }));
        entity.add(Movement({ x: 1, y: 2, vx: 3, vy: 4 }));

        expect(entity.has(Movement)).toBe(true);
        expect(entity.has(Position)).toBe(true);
        expect(entity.has(Velocity)).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 5, y: 6 });
        expect(entity.get(Velocity)).toEqual({ vx: 3, vy: 4 });
        expect(entity.get(Movement)).toEqual({ x: 5, y: 6, vx: 3, vy: 4 });

        entity.set(Movement, { x: 9, vy: 8 });
        expect(entity.get(Position)).toEqual({ x: 9, y: 6 });
        expect(entity.get(Velocity)).toEqual({ vx: 3, vy: 8 });

        entity.set(Movement, (prev) => ({ ...prev!, x: prev!.x + 1 }));
        expect(entity.get(Movement)).toEqual({ x: 10, y: 6, vx: 3, vy: 8 });

        entity.remove(Movement);
        expect(entity.has(Position)).toBe(false);
        expect(entity.has(Velocity)).toBe(false);
        expect(entity.has(Movement)).toBe(false);
        expect(entity.get(Movement)).toBeUndefined();

        expectTypeOf(entity.get(Movement)).toEqualTypeOf<
            { x: number; y: number; vx: number; vy: number } | undefined
        >();
    });

    it('spawns with initial values distributed by field', () => {
        const Movement = createAspect(Position, Velocity);
        const entity = world.spawn(Movement({ x: 1, vy: 2 }));

        expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        expect(entity.get(Velocity)).toEqual({ vx: 0, vy: 2 });
    });

    it('merges AoS fields without replacing the instance', () => {
        const Body = createAspect(Position, Health);
        const entity = world.spawn(Body({ x: 3, hp: 7 }));
        const health = entity.get(Health);

        expect(entity.get(Body)).toEqual({ x: 3, y: 0, hp: 7 });

        world.query(Body).updateEach(([body]) => {
            body.x += 1;
            body.hp -= 2;
        });

        expect(entity.get(Position)).toEqual({ x: 4, y: 0 });
        expect(entity.get(Health)).toBe(health);
        expect(health!.hp).toBe(5);
    });

    it('requires every constituent in a query and merges read/update', () => {
        const Movement = createAspect(Position, Velocity);
        const partial = world.spawn(Position({ x: 1, y: 1 }));
        const full = world.spawn(Movement({ x: 2, y: 3, vx: 4, vy: 5 }));

        const matched = world.query(Movement);
        expect(matched).toContain(full);
        expect(matched).not.toContain(partial);

        const reads: unknown[] = [];
        world.query(Movement).readEach(([movement]) => {
            reads.push({ ...movement });
            movement.x = 100;
        });

        expect(reads).toEqual([{ x: 2, y: 3, vx: 4, vy: 5 }]);
        expect(full.get(Position)!.x).toBe(2);

        world.query(Movement, IsActive).updateEach(([movement]) => {
            movement.x += movement.vx;
            movement.vy = 9;
        });
        expect(full.get(Position)!.x).toBe(2);

        full.add(IsActive);
        world.query(Movement, IsActive).updateEach(([movement]) => {
            movement.x += movement.vx;
            movement.vy = 9;
        });

        expect(full.get(Position)).toEqual({ x: 6, y: 3 });
        expect(full.get(Velocity)).toEqual({ vx: 4, vy: 9 });
    });

    it('composes with Not, Or, and tracking modifiers', () => {
        const Movement = createAspect(Position, Velocity);
        const partial = world.spawn(Position);
        const tagged = world.spawn(IsActive);
        const full = world.spawn(Movement, IsDead);
        const moving = world.spawn(Movement);

        expect(world.query(Not(Movement))).toEqual(expect.arrayContaining([partial, tagged]));
        expect(world.query(Not(Movement))).not.toContain(full);
        expect([...world.query(Position, Not(Movement))]).toEqual([partial]);
        expect(world.query(Or(Movement, IsActive))).toEqual(
            expect.arrayContaining([tagged, full, moving])
        );
        expect(world.query(Or(Movement, IsActive))).not.toContain(partial);
        expect([...world.query(Movement, Not(IsDead))]).toEqual([moving]);

        const Added = createAdded();
        const late = world.spawn();
        late.add(Position);
        expect([...world.query(Added(Movement))]).toEqual([]);
        late.add(Velocity);
        expect([...world.query(Added(Movement))]).toEqual([late]);

        const Removed = createRemoved();
        moving.remove(Velocity);
        expect([...world.query(Removed(Movement))]).toEqual([moving]);
        expect([...world.query(Removed(Movement), Position)]).toEqual([moving]);

        const Changed = createChanged();
        full.set(Position, { x: 1 });
        expect([...world.query(Changed(Movement))]).toEqual([full]);
        expect([...world.query(Changed(Position))]).toEqual([full]);
        expect([...world.query(Changed(Velocity))]).toEqual([]);

        partial.set(Position, { x: 4 });
        expect([...world.query(Changed(Movement))]).toEqual([]);
    });

    it('does not report Added or Removed for entities that were already complete', () => {
        const entity = world.spawn(Position, Velocity);
        const Added = createAdded();
        const Removed = createRemoved();
        const Movement = createAspect(Position, Velocity);

        expect(entity.has(Movement)).toBe(true);
        expect(world.query(Movement)).toContain(entity);
        expect([...world.query(Added(Movement))]).toEqual([]);
        expect([...world.query(Removed(Movement))]).toEqual([]);

        entity.remove(Position);
        expect([...world.query(Removed(Movement))]).toEqual([entity]);
    });

    it('fires lifecycle hooks on completeness transitions', () => {
        const Movement = createAspect(Position, Velocity);
        const added: number[] = [];
        const removed: number[] = [];
        const changed: number[] = [];
        const positionChanged: number[] = [];

        world.onAdd(Movement, (entity) => {
            expect(entity.has(Movement)).toBe(true);
            expect(entity.get(Movement)).toBeDefined();
            added.push(entity);
        });
        world.onRemove(Movement, (entity) => {
            expect(entity.has(Movement)).toBe(true);
            expect(entity.get(Position)).toBeDefined();
            expect(entity.get(Velocity)).toBeDefined();
            removed.push(entity);
        });
        world.onChange(Movement, (entity) => changed.push(entity));
        world.onChange(Position, (entity) => positionChanged.push(entity));

        const entity = world.spawn();
        entity.add(Position({ x: 1, y: 2 }));
        expect(added).toEqual([]);
        entity.set(Position, { x: 9 });
        expect(changed).toEqual([]);

        entity.add(Velocity({ vx: 3, vy: 4 }));
        expect(added).toEqual([entity]);
        expect(changed).toEqual([]);

        positionChanged.length = 0;
        entity.set(Movement, { x: 5, vx: 6 });
        expect(changed).toEqual([entity]);
        expect(positionChanged).toEqual([entity]);
        expect(entity.get(Velocity)!.vx).toBe(6);

        const unsub = world.onAdd(Movement, () => added.push(-1));
        unsub();
        entity.remove(Position);
        expect(removed).toEqual([entity]);
        expect(entity.has(Movement)).toBe(false);

        entity.add(Position);
        expect(added).toEqual([entity, entity]);

        entity.destroy();
        expect(removed).toEqual([entity, entity]);
    });

    it('notifies every complete aspect that shares a changed constituent', () => {
        const A = trait({ a: 0 });
        const B = trait({ b: 0 });
        const C = trait({ c: 0 });
        const AB = createAspect(A, B);
        const AC = createAspect(A, C);
        const ab: number[] = [];
        const ac: number[] = [];

        world.onChange(AB, (entity) => ab.push(entity));
        world.onChange(AC, (entity) => ac.push(entity));

        const entity = world.spawn(AB);
        entity.set(A, { a: 1 });
        expect(ab).toEqual([entity]);
        expect(ac).toEqual([]);

        entity.add(C);
        entity.set(AB, { a: 2, b: 3 });
        expect(ab).toEqual([entity, entity]);
        expect(ac).toEqual([entity]);
    });

    it('updateEach change detection follows observers and the never option', () => {
        const Movement = createAspect(Position, Velocity);
        const entity = world.spawn(Movement);
        const changes: number[] = [];
        world.onChange(Movement, (target) => changes.push(target));

        world.query(Movement).updateEach(
            ([movement]) => {
                movement.x = 1;
            },
            { changeDetection: 'never' }
        );
        expect(entity.get(Position)!.x).toBe(1);
        expect(changes).toEqual([]);

        world.query(Movement).updateEach(([movement]) => {
            movement.vx = 2;
        });
        expect(entity.get(Velocity)!.vx).toBe(2);
        expect(changes).toEqual([entity]);

        const Changed = createChanged();
        world.query(Movement).updateEach(
            ([movement]) => {
                movement.y = 3;
            },
            { changeDetection: 'always' }
        );
        expect([...world.query(Changed(Movement))]).toEqual([entity]);
        expect([...world.query(Changed(Position))]).toEqual([entity]);
    });

    it('tracks constituents on worlds created after the aspect', () => {
        const Movement = createAspect(Position, Velocity);
        const later = createWorld();
        const entity = later.spawn(Position({ x: 3 }), Velocity({ vy: 4 }));

        expect(entity.has(Movement)).toBe(true);
        expect(entity.get(Movement)).toEqual({ x: 3, y: 0, vx: 0, vy: 4 });
        expect([...later.query(Movement)]).toEqual([entity]);

        const Changed = createChanged();
        entity.set(Position, { y: 1 });
        expect([...later.query(Changed(Movement))]).toEqual([entity]);

        later.destroy();
    });

    it('works through world entity accessors and survives reset', () => {
        const Movement = createAspect(Position, Velocity);

        world.add(Movement({ x: 1, vx: 2 }));
        expect(world.has(Movement)).toBe(true);
        expect(world.get(Movement)).toEqual({ x: 1, y: 0, vx: 2, vy: 0 });
        world.set(Movement, { y: 4 });
        expect(world.get(Position)).toEqual({ x: 1, y: 4 });
        world.remove(Movement);
        expect(world.has(Position)).toBe(false);

        world.spawn(Movement);
        world.reset();
        expect([...world.query(Movement)]).toEqual([]);

        const next = world.spawn(Movement({ x: 8 }));
        expect(next.get(Movement)).toEqual({ x: 8, y: 0, vx: 0, vy: 0 });
        expect([...world.query(Movement)]).toEqual([next]);
    });
});
