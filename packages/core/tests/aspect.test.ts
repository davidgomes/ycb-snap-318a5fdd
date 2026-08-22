import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    createAdded,
    createAspect,
    createChanged,
    createRemoved,
    createWorld,
    Not,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ vx: 0, vy: 0 });
const Health = trait({ value: 100 });
const IsActive = trait();

describe('Aspects', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('createAspect exposes id, traits, and schema', () => {
        const Transform = createAspect(Position, Velocity);

        expect(Transform.id).toBeTypeOf('number');
        expect(Transform.traits).toEqual([Position, Velocity]);
        expect(Transform.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
    });

    it('each createAspect call returns a distinct instance', () => {
        const a = createAspect(Position, Velocity);
        const b = createAspect(Position, Velocity);

        expect(a).not.toBe(b);
        expect(a.id).not.toBe(b.id);
    });

    it('throws on overlapping field names', () => {
        const Other = trait({ x: 0, z: 0 });
        expect(() => createAspect(Position, Other)).toThrow(/overlapping field name "x"/);
    });

    it('throws on relation constituents', () => {
        const ChildOf = relation();
        expect(() => createAspect(Position, ChildOf as any)).toThrow(/relation constituents/);
    });

    it('allows tag traits as constituents', () => {
        const Player = createAspect(Position, IsActive);
        expect(Player.traits).toEqual([Position, IsActive]);
        expect(Player.schema).toEqual({ x: 0, y: 0 });
    });

    it('flattens nested aspects', () => {
        const Motion = createAspect(Position, Velocity);
        const Body = createAspect(Motion, Health);

        expect(Body.traits).toEqual([Position, Velocity, Health]);
        expect(Body.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0, value: 100 });
    });

    it('supports has, get, set, add, and remove on entities', () => {
        const Transform = createAspect(Position, Velocity, IsActive);
        const entity = world.spawn();

        expect(entity.has(Transform)).toBe(false);

        entity.add([Transform, { x: 5, y: 6, vx: 1, vy: 2 }]);
        expect(entity.has(Transform)).toBe(true);
        expect(entity.get(Transform)).toEqual({ x: 5, y: 6, vx: 1, vy: 2 });

        entity.set(Transform, (prev) => ({ ...prev!, x: 10 }));
        expect(entity.get(Position)).toEqual({ x: 10, y: 6 });
        expect(entity.get(Velocity)).toEqual({ vx: 1, vy: 2 });

        entity.remove(Velocity);
        expect(entity.has(Transform)).toBe(false);
        expect(entity.get(Transform)).toBeUndefined();

        entity.add(Velocity);
        entity.add([Transform, { vx: 3 }]);
        expect(entity.get(Transform)).toEqual({ x: 10, y: 6, vx: 3, vy: 0 });

        entity.remove(Transform);
        expect(entity.has(Position)).toBe(false);
        expect(entity.has(Velocity)).toBe(false);
        expect(entity.has(IsActive)).toBe(false);
    });

    it('queries require all constituents and return merged data', () => {
        const Transform = createAspect(Position, Velocity);
        const entity = world.spawn(Position({ x: 1, y: 2 }), Velocity({ vx: 3, vy: 4 }));
        world.spawn(Position);

        const entities = world.query(Transform);
        expect(entities).toHaveLength(1);
        expect(entities[0]).toBe(entity);

        entities.readEach(([state]) => {
            expect(state).toEqual({ x: 1, y: 2, vx: 3, vy: 4 });
        });

        entities.updateEach(([state]) => {
            state.x = 10;
            state.vy = 40;
        });

        expect(entity.get(Transform)).toEqual({ x: 10, y: 2, vx: 3, vy: 40 });
    });

    it('Not(aspect) matches entities missing at least one constituent', () => {
        const Transform = createAspect(Position, Velocity);
        const both = world.spawn(Position, Velocity);
        const positionOnly = world.spawn(Position);
        const neither = world.spawn();

        const missing = world.query(Not(Transform));
        expect(missing).toContain(positionOnly);
        expect(missing).toContain(neither);
        expect(missing).not.toContain(both);
    });

    it('supports Added, Removed, and Changed modifiers with aspects', () => {
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();
        const Transform = createAspect(Position, Velocity);

        const entity = world.spawn(Position);
        entity.add(Velocity);

        expect(world.query(Added(Transform))[0]).toBe(entity);
        expect(world.query(Added(Transform))).toHaveLength(0);

        entity.set(Velocity, { vx: 1, vy: 1 });
        entity.changed(Velocity);
        expect(world.query(Changed(Transform))[0]).toBe(entity);

        entity.remove(Velocity);
        expect(world.query(Removed(Transform))[0]).toBe(entity);
    });

    it('fires aspect onAdd, onRemove, and onChange hooks', () => {
        const Transform = createAspect(Position, Velocity);
        const onAdd = vi.fn();
        const onRemove = vi.fn();
        const onChange = vi.fn();

        world.onAdd(Transform, onAdd);
        world.onRemove(Transform, onRemove);
        world.onChange(Transform, onChange);

        const entity = world.spawn(Position);
        entity.add(Velocity);
        expect(onAdd).toHaveBeenCalledWith(entity);
        expect(onAdd).toHaveBeenCalledTimes(1);

        entity.set(Velocity, { vx: 1, vy: 1 });
        expect(onChange).toHaveBeenCalledWith(entity);

        entity.remove(Velocity);
        expect(onRemove).toHaveBeenCalledWith(entity);
    });
});
