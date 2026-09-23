---
'@tanstack/query-core': minor
'@tanstack/query-persist-client-core': patch
---

feat(query-core): add `createPersisterRestoreResult` so persisters can restore a full query snapshot; fine-grained persisted queries now keep their error state, failure counters, timestamps, invalidation markers and infinite-query page params when restored, both per query and via `restoreQueries`
