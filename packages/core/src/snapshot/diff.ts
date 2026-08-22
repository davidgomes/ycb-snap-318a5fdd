import type { EntitySnapshot, EntitySnapshotDiff, WorldSnapshot, WorldSnapshotDiff } from './types';
import { entitySnapshotsEqual, traitValuesEqual } from './utils';

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) {
        throw new Error('Koota: Entity snapshots must not be null or undefined.');
    }

    const traitKeysA = new Set(Object.keys(a.traits));
    const traitKeysB = new Set(Object.keys(b.traits));

    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of traitKeysB) {
        if (!traitKeysA.has(key)) addedTraits.push(key);
    }

    for (const key of traitKeysA) {
        if (!traitKeysB.has(key)) {
            removedTraits.push(key);
            continue;
        }

        if (!traitValuesEqual(a.traits[key]!, b.traits[key]!)) {
            changedTraits.push(key);
        }
    }

    addedTraits.sort();
    removedTraits.sort();
    changedTraits.sort();

    return { addedTraits, removedTraits, changedTraits };
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    if (before == null || after == null || !Array.isArray(before.entities) || !Array.isArray(after.entities)) {
        throw new Error('Koota: World snapshots must include an entities array.');
    }

    const beforeById = new Map(before.entities.map((snapshot) => [snapshot.id, snapshot]));
    const afterById = new Map(after.entities.map((snapshot) => [snapshot.id, snapshot]));

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const id of afterById.keys()) {
        if (!beforeById.has(id)) added.push(id);
    }

    for (const id of beforeById.keys()) {
        if (!afterById.has(id)) {
            removed.push(id);
            continue;
        }

        if (!entitySnapshotsEqual(beforeById.get(id)!, afterById.get(id)!)) {
            changed.push(id);
        }
    }

    added.sort((left, right) => left - right);
    removed.sort((left, right) => left - right);
    changed.sort((left, right) => left - right);

    return { added, removed, changed };
}
