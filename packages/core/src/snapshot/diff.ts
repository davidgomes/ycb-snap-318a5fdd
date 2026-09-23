import { shallowEqual } from '../utils/shallow-equal';
import type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationTargetSnapshot,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) throw new Error('Koota: diffEntitySnapshots requires two entity snapshots.');

    const before = a.traits ?? {};
    const after = b.traits ?? {};
    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of Object.keys(before)) {
        if (!Object.hasOwn(after, key)) removedTraits.push(key);
        else if (!shallowEqual(before[key], after[key])) changedTraits.push(key);
    }

    for (const key of Object.keys(after)) {
        if (!Object.hasOwn(before, key)) addedTraits.push(key);
    }

    return {
        addedTraits: addedTraits.sort(),
        removedTraits: removedTraits.sort(),
        changedTraits: changedTraits.sort(),
    };
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (!Array.isArray(before?.entities) || !Array.isArray(after?.entities)) {
        throw new Error('Koota: diffWorldSnapshots requires two world snapshots with an entities array.');
    }

    const beforeById = new Map(before.entities.map((snapshot) => [snapshot.id, snapshot]));
    const afterById = new Map(after.entities.map((snapshot) => [snapshot.id, snapshot]));
    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, snapshot] of beforeById) {
        const next = afterById.get(id);
        if (!next) removed.push(id);
        else if (!entitySnapshotsEqual(snapshot, next)) changed.push(id);
    }

    for (const id of afterById.keys()) {
        if (!beforeById.has(id)) added.push(id);
    }

    const ascending = (x: number, y: number) => x - y;

    return {
        added: added.sort(ascending),
        removed: removed.sort(ascending),
        changed: changed.sort(ascending),
    };
}

function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot): boolean {
    return (
        recordsEqual(a.traits ?? {}, b.traits ?? {}, shallowEqual) &&
        recordsEqual(getRelations(a), getRelations(b), targetsEqual)
    );
}

function recordsEqual<T>(
    a: Record<string, T>,
    b: Record<string, T>,
    equal: (x: T, y: T) => boolean
): boolean {
    const keys = Object.keys(a);
    return (
        keys.length === Object.keys(b).length &&
        keys.every((key) => Object.hasOwn(b, key) && equal(a[key], b[key]))
    );
}

/** A relation without targets is the same as no relation. */
function getRelations(snapshot: EntitySnapshot): Record<string, RelationTargetSnapshot[]> {
    const relations: Record<string, RelationTargetSnapshot[]> = {};

    for (const [key, targets] of Object.entries(snapshot.relations ?? {})) {
        if (targets.length > 0) relations[key] = targets;
    }

    return relations;
}

function targetsEqual(a: RelationTargetSnapshot[], b: RelationTargetSnapshot[]): boolean {
    if (a.length !== b.length) return false;

    const unmatched = new Map<number, Array<object | undefined>>();
    for (const { targetId, data } of b) {
        const group = unmatched.get(targetId);
        if (group) group.push(data);
        else unmatched.set(targetId, [data]);
    }

    for (const { targetId, data } of a) {
        const group = unmatched.get(targetId);
        const index = group ? group.findIndex((other) => shallowEqual(other, data)) : -1;
        if (index === -1) return false;
        group!.splice(index, 1);
    }

    return true;
}
