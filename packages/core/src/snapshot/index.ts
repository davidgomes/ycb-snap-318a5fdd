export { createTraitRegistry } from './trait-registry';
export { snapshotEntity, snapshotWorld } from './snapshot';
export { rollbackEntity, rollbackWorld } from './rollback';
export { diffEntitySnapshots, diffWorldSnapshots } from './diff';
export type {
    EntitySnapshot,
    EntitySnapshotDiff,
    RelationTargetSnapshot,
    TraitRegistry,
    TraitRegistryEntry,
    WorldSnapshot,
    WorldSnapshotDiff,
} from './types';
