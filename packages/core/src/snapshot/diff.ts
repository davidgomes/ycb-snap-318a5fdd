import { shallowEqual } from '../utils/shallow-equal';
import type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationTargetSnapshot,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';

const byNumber = (a: number, b: number) => a - b;

export function diffEntitySnapshots(a: EntitySnapshot, b: EntitySnapshot): EntitySnapshotDiff {
    if (a == null || b == null) {
        throw new Error('Koota: diffEntitySnapshots requires two entity snapshots.');
    }

    const before = a.traits ?? {};
    const after = b.traits ?? {};

    const addedTraits: string[] = [];
    const removedTraits: string[] = [];
    const changedTraits: string[] = [];

    for (const key of Object.keys(after)) {
        if (!Object.hasOwn(before, key)) addedTraits.push(key);
        else if (!shallowEqual(before[key], after[key])) changedTraits.push(key);
    }

    for (const key of Object.keys(before)) {
        if (!Object.hasOwn(after, key)) removedTraits.push(key);
    }

    return {
        addedTraits: addedTraits.sort(),
        removedTraits: removedTraits.sort(),
        changedTraits: changedTraits.sort(),
    };
}

function getNonEmptyRelations(snapshot: EntitySnapshot) {
    const relations = new Map<string, RelationTargetSnapshot[]>();
    if (!snapshot.relations) return relations;
    for (const key of Object.keys(snapshot.relations)) {
        const targets = snapshot.relations[key];
        if (targets && targets.length > 0) relations.set(key, targets);
    }
    return relations;
}

function relationTargetsEqual(a: RelationTargetSnapshot[], b: RelationTargetSnapshot[]) {
    if (a.length !== b.length) return false;

    const dataByTarget = new Map<number, object | undefined>();
    for (const { targetId, data } of a) dataByTarget.set(targetId, data);
    if (dataByTarget.size !== a.length) return false;

    const matched = new Set<number>();
    for (const { targetId, data } of b) {
        if (!dataByTarget.has(targetId) || matched.has(targetId)) return false;
        if (!shallowEqual(dataByTarget.get(targetId), data)) return false;
        matched.add(targetId);
    }

    return true;
}

function entitySnapshotsEqual(a: EntitySnapshot, b: EntitySnapshot) {
    const traitsA = a.traits ?? {};
    const traitsB = b.traits ?? {};
    const traitKeysA = Object.keys(traitsA);

    if (traitKeysA.length !== Object.keys(traitsB).length) return false;
    for (const key of traitKeysA) {
        if (!Object.hasOwn(traitsB, key) || !shallowEqual(traitsA[key], traitsB[key])) return false;
    }

    const relationsA = getNonEmptyRelations(a);
    const relationsB = getNonEmptyRelations(b);

    if (relationsA.size !== relationsB.size) return false;
    for (const [key, targets] of relationsA) {
        const other = relationsB.get(key);
        if (!other || !relationTargetsEqual(targets, other)) return false;
    }

    return true;
}

function assertWorldSnapshot(snapshot: WorldSnapshot, name: string) {
    if (snapshot == null || !Array.isArray(snapshot.entities)) {
        throw new Error(`Koota: diffWorldSnapshots requires "${name}" to have an entities array.`);
    }
}

export function diffWorldSnapshots(before: WorldSnapshot, after: WorldSnapshot): WorldSnapshotDiff {
    assertWorldSnapshot(before, 'before');
    assertWorldSnapshot(after, 'after');

    const beforeById = new Map(before.entities.map((entity) => [entity.id, entity]));
    const afterById = new Map(after.entities.map((entity) => [entity.id, entity]));

    const added: number[] = [];
    const removed: number[] = [];
    const changed: number[] = [];

    for (const [id, entity] of afterById) {
        const previous = beforeById.get(id);
        if (!previous) added.push(id);
        else if (!entitySnapshotsEqual(previous, entity)) changed.push(id);
    }

    for (const id of beforeById.keys()) {
        if (!afterById.has(id)) removed.push(id);
    }

    return {
        added: added.sort(byNumber),
        removed: removed.sort(byNumber),
        changed: changed.sort(byNumber),
    };
}
