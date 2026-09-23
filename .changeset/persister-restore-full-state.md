---
'@tanstack/query-core': minor
'@tanstack/query-persist-client-core': patch
---

feat(query-core): add `createPersisterRestoreResult` so persisters can restore a full query state snapshot; fine-grained persisted queries now keep their errors, counters, timestamps and invalidation on restore, and `restoreQueries` merges data and error freshness independently with existing queries
