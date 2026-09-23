import { beforeEach, describe, expect, it } from 'vitest';
import { createAdded, createChanged, createRemoved, createWorld, Or, relation, trait } from '../src';

const Added = createAdded();
const Removed = createRemoved();
const Changed = createChanged();

const Likes = relation();
const Owes = relation({ store: { amount: 0 } });
const ChildOf = relation({ exclusive: true });
const Tag = trait();

describe('Pair tracking modifiers', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('detects non-first pair additions per target', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e = world.spawn(Likes(a));
        expect([...world.query(Added(Likes(a)))]).toEqual([e]);
        expect([...world.query(Added(Likes(a)))]).toEqual([]);

        e.add(Likes(b));
        expect([...world.query(Added(Likes(b)))]).toEqual([e]);
        expect([...world.query(Added(Likes(a)))]).toEqual([]);
        expect([...world.query(Added(Likes('*')))]).toEqual([e]);
    });

    it('detects non-last pair removals', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e = world.spawn(Likes(a), Likes(b));
        world.query(Removed(Likes(a)));

        e.remove(Likes(a));
        expect(e.targetsFor(Likes)).toEqual([b]);
        expect([...world.query(Removed(Likes(a)))]).toEqual([e]);
        expect([...world.query(Removed(Likes(b)))]).toEqual([]);
    });

    it('exclusive replacement produces removal and addition', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e = world.spawn(ChildOf(a));
        world.query(Removed(ChildOf(a)));
        world.query(Added(ChildOf(b)));

        e.add(ChildOf(b));
        expect([...world.query(Removed(ChildOf(a)))]).toEqual([e]);
        expect([...world.query(Added(ChildOf(b)))]).toEqual([e]);
    });

    it('cancels opposite events on the same target', () => {
        const a = world.spawn();
        const e = world.spawn();
        world.query(Added(Likes(a)));
        e.add(Likes(a));
        e.remove(Likes(a));
        expect([...world.query(Added(Likes(a)))]).toEqual([]);
    });

    it('fires pair removal on destroy', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e = world.spawn(Likes(a), Likes(b));
        world.query(Removed(Likes(a)));
        world.query(Removed(Likes(b)));
        e.destroy();
        expect([...world.query(Removed(Likes(a)))]).toEqual([e]);
        expect([...world.query(Removed(Likes(b)))]).toEqual([e]);
    });

    it('composes with Or and regular traits', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e1 = world.spawn(Likes(a));
        const e2 = world.spawn(Likes(b), Tag);
        expect([...world.query(Or(Added(Likes(a)), Added(Likes(b)))).sort()]).toEqual([e1, e2]);

        world.query(Added(Likes(b)), Tag);
        world.query(Added(Likes(a)), Tag);
        const e3 = world.spawn(Likes(a));
        e1.remove(Likes(a));
        e1.add(Tag, Likes(b));
        expect([...world.query(Added(Likes(b)), Tag)]).toEqual([e1]);
        expect([...world.query(Added(Likes(a)), Tag)]).toEqual([]);
        expect([...world.query(Added(Likes(a)))]).toContain(e3);
    });

    it('distinguishes cached queries by target', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e = world.spawn(Likes(a));
        expect([...world.query(Added(Likes(a)))]).toEqual([e]);
        expect([...world.query(Added(Likes(b)))]).toEqual([]);
    });

    it('tracks pair changes and resolves per-target data', () => {
        const a = world.spawn();
        const b = world.spawn();
        const e = world.spawn(Owes(a, { amount: 1 }), Owes(b, { amount: 2 }));
        world.query(Changed(Owes(b)));

        e.set(Owes(b), { amount: 5 });
        const values: number[] = [];
        world.query(Changed(Owes(b))).readEach(([owes], entity) => {
            expect(entity).toBe(e);
            values.push(owes.amount);
        });
        expect(values).toEqual([5]);
        expect([...world.query(Changed(Owes(a)))]).toEqual([]);

        world.query(Changed(Owes(a)));
        e.changed(Owes(a));
        expect([...world.query(Changed(Owes(a)))]).toEqual([e]);
    });

    it('works after world reset', () => {
        const a = world.spawn();
        const e = world.spawn(Likes(a));
        expect([...world.query(Added(Likes(a)))]).toEqual([e]);
    });
});
