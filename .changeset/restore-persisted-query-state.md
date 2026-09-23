---
'@tanstack/query-core': minor
'@tanstack/query-persist-client-core': minor
---

feat(query-core): add `createPersisterRestoreResult` so a `persister` can restore a query snapshot instead of reporting a fresh fetch

fix(query-persist-client-core): `experimental_createQueryPersister` now restores the full persisted query state (errors, failure counts, timestamps, invalidation, page params), and `restoreQueries` merges data and error freshness independently with queries already in the cache
