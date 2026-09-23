import { $internal } from '../../common';
import type { World } from '../../world';

// Some values are reserved.
// 0 - has
// 1 - not
// 2 - or
let cursor = 3;

const trackingKinds = new Map<number, 'add' | 'remove' | 'change'>();

export function createTrackingId(kind: 'add' | 'remove' | 'change') {
    const id = cursor++;
    trackingKinds.set(id, kind);
    return id;
}

export function getTrackingKind(id: number) {
    return trackingKinds.get(id);
}

export function getTrackingCursor() {
    return cursor;
}

export function setTrackingMasks(world: World, id: number) {
    const ctx = world[$internal];
    const snapshot = structuredClone(ctx.entityMasks);
    ctx.trackingSnapshots.set(id, snapshot);

    // For dirty and changed masks, make clone of entity masks and set all bits to 0.
    ctx.dirtyMasks.set(
        id,
        snapshot.map((mask) => mask.map(() => 0))
    );

    ctx.changedMasks.set(
        id,
        snapshot.map((mask) => mask.map(() => 0))
    );

    const kind = trackingKinds.get(id);
    if (kind) ctx.pairTrackers.set(id, { kind, nets: new Map() });
}
