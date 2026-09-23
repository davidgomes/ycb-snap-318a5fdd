import { beforeEach, describe, expect, it, vi } from 'vitest';
import { $internal, createAdded, createAspect, createChanged, createRemoved, createWorld, Not, Or, relation, trait } from '../src';

const Position = trait({ x: 0, y: 0 });
const Health = trait({ hp: 100 });
const Name = trait({ name: '' });
const IsPlayer = trait();
const IsEnemy = trait();

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('exposes id, traits, and schema and returns a distinct instance per call', () => {
        const movement = createAspect(Position, Health);
        const again = createAspect(Position, Health);

        expect(movement).not.toBe(again);
        expect(movement.id).not.toBe(again.id);
        expect(movement.traits).toEqual([Position, Health]);
        expect(movement.schema).toEqual({ x: 0, y: 0, hp: 100 });
        expect(Object.keys(movement)).toEqual(expect.arrayContaining(['id', 'traits', 'schema']));
    });

    it('flattens nested aspects', () => {
        const body = createAspect(Position, Health);
        const actor = createAspect(body, Name);

        expect(actor.traits).toEqual([Position, Health, Name]);
        expect(actor.schema).toEqual({ x: 0, y: 0, hp: 100, name: '' });
        expect(createAspect(body).traits).toEqual([Position, Health]);
    });

    it('accepts tag traits', () => {
        const flags = createAspect(IsPlayer, IsEnemy);
        const entity = world.spawn(flags);

        expect(flags.schema).toEqual({});
        expect(entity.has(flags)).toBe(true);
        expect(entity.has(IsPlayer)).toBe(true);
        expect(entity.has(IsEnemy)).toBe(true);
        expect(entity.get(flags)).toEqual({});
    });

    it('throws when constituents overlap, repeat, are relations, are AoS, or are too few', () => {
        expect(() => createAspect(Position)).toThrow(/two or more/);
        expect(() => createAspect(Position, Position)).toThrow(/unique/);
        expect(() => createAspect(Position, trait({ x: 1 }))).toThrow(/overlapping field "x"/);

        const ChildOf = relation();
        expect(() => createAspect(Position, ChildOf as never)).toThrow(/relation/);
        expect(() => createAspect(Position, ChildOf[$internal].trait)).toThrow(/relation/);
        expect(() => createAspect(Position, trait(() => ({ mesh: true })))).toThrow(/AoS/);
    });

    it('has, get, set, add, and remove operate on every constituent', () => {
        const actor = createAspect(Position, Health, IsPlayer);
        const entity = world.spawn();

        expect(entity.has(actor)).toBe(false);
        expect(entity.get(actor)).toBeUndefined();

        entity.add(actor({ x: 4, hp: 7 }));
        expect(entity.has(actor)).toBe(true);
        expect(entity.has(IsPlayer)).toBe(true);
        expect(entity.get(actor)).toEqual({ x: 4, y: 0, hp: 7 });

        entity.set(actor, { x: 8 });
        expect(entity.get(Position)).toEqual({ x: 8, y: 0 });
        expect(entity.get(Health)).toEqual({ hp: 7 });

        entity.set(actor, (prev) => ({ ...prev, hp: (prev?.hp ?? 0) + 1 }));
        expect(entity.get(actor)?.hp).toBe(8);

        const partial = world.spawn(Position({ x: 5, y: 6 }));
        partial.add(actor({ x: 1, y: 2, hp: 9 }));
        expect(partial.get(Position)).toEqual({ x: 5, y: 6 });
        expect(partial.get(Health)).toEqual({ hp: 9 });
        expect(partial.has(actor)).toBe(true);

        partial.remove(actor);
        expect(partial.has(Position)).toBe(false);
        expect(partial.has(Health)).toBe(false);
        expect(partial.has(IsPlayer)).toBe(false);
        expect(partial.has(actor)).toBe(false);
    });

    it('queries require every constituent and merge data for read and update', () => {
        const actor = createAspect(Position, Health);
        const full = world.spawn(actor({ x: 1, y: 2, hp: 3 }), Name({ name: 'ada' }));
        const partial = world.spawn(Position);
        world.spawn();

        const found = world.query(actor);
        expect(found).toContain(full);
        expect(found).not.toContain(partial);
        expect(found.length).toBe(1);

        world.query(actor, Name).readEach(([data, name]) => {
            expect(data).toEqual({ x: 1, y: 2, hp: 3 });
            expect(name).toEqual({ name: 'ada' });
        });

        world.query(actor).updateEach(([data]) => {
            data.x += 10;
            data.hp += 4;
        });

        expect(full.get(Position)).toEqual({ x: 11, y: 2 });
        expect(full.get(Health)).toEqual({ hp: 7 });
    });

    it('composes with Not, Or, and other modifiers', () => {
        const actor = createAspect(Position, Health);
        const full = world.spawn(actor);
        const hostile = world.spawn(actor, IsEnemy);
        const named = world.spawn(Name({ name: 'bee' }));
        const partial = world.spawn(Position);
        const empty = world.spawn();

        const missing = world.query(Not(actor));
        expect(missing).toContain(partial);
        expect(missing).toContain(empty);
        expect(missing).toContain(named);
        expect(missing).not.toContain(full);
        expect(missing).not.toContain(hostile);

        const either = world.query(Or(actor, Name));
        expect(either).toContain(full);
        expect(either).toContain(hostile);
        expect(either).toContain(named);
        expect(either).not.toContain(partial);

        const friendly = world.query(actor, Not(IsEnemy));
        expect(friendly).toContain(full);
        expect(friendly).not.toContain(hostile);

        const Added = createAdded();
        const withName = world.spawn(actor, Name({ name: 'cy' }));
        const addedNamed = world.query(Added(actor), Name);
        expect(addedNamed).toContain(withName);
        expect(addedNamed).not.toContain(named);
    });

    it('tracks added, removed, and changed transitions for the whole aspect', () => {
        const actor = createAspect(Position, Health);
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();

        const building = world.spawn();
        expect(world.query(Added(actor)).length).toBe(0);

        building.add(Position);
        expect(world.query(Added(actor)).length).toBe(0);
        building.add(Health);
        expect(world.query(Added(actor))).toContain(building);
        expect(world.query(Added(actor)).length).toBe(0);

        const complete = world.spawn(actor({ x: 2, y: 3, hp: 4 }));
        expect(world.query(Added(actor))).toContain(complete);

        expect(world.query(Removed(actor)).length).toBe(0);
        complete.remove(Health);
        expect(world.query(Removed(actor))).toContain(complete);
        expect(complete.has(Position)).toBe(true);
        expect(world.query(Removed(actor)).length).toBe(0);

        const watched = world.spawn(actor({ x: 1, y: 1, hp: 1 }));
        expect(world.query(Changed(actor)).length).toBe(0);
        watched.set(actor, { hp: 5 });
        expect(world.query(Changed(actor))).toContain(watched);
        expect(world.query(Changed(actor)).length).toBe(0);

        const onlyPosition = world.spawn(Position);
        onlyPosition.set(Position, { x: 3 });
        expect(world.query(Changed(actor))).not.toContain(onlyPosition);

        watched.set(Position, { y: 9 });
        world.query(Changed(actor)).updateEach(([data]) => {
            expect(data.y).toBe(9);
            data.x = 40;
        });
        expect(watched.get(Position)?.x).toBe(40);
    });

    it('fires onAdd, onRemove, and onChange on completeness transitions', () => {
        const actor = createAspect(Position, Health);
        const onAdd = vi.fn();
        const onRemove = vi.fn();
        const onChange = vi.fn();

        const stopAdd = world.onAdd(actor, onAdd);
        world.onRemove(actor, onRemove);
        world.onChange(actor, onChange);

        const entity = world.spawn(Position);
        expect(onAdd).not.toHaveBeenCalled();

        entity.add(Health);
        expect(onAdd).toHaveBeenCalledTimes(1);
        expect(onAdd).toHaveBeenCalledWith(entity);

        entity.set(Position, { x: 2 });
        expect(onChange).toHaveBeenCalledTimes(1);

        entity.set(actor, { x: 3, hp: 4 });
        expect(onChange).toHaveBeenCalledTimes(3);

        entity.remove(Health);
        expect(onRemove).toHaveBeenCalledTimes(1);
        expect(onRemove).toHaveBeenCalledWith(entity);

        entity.remove(Position);
        expect(onRemove).toHaveBeenCalledTimes(1);

        entity.set(Position, { x: 1 });
        expect(onChange).toHaveBeenCalledTimes(3);

        stopAdd();
        entity.add(Position, Health);
        expect(onAdd).toHaveBeenCalledTimes(1);
    });
});
