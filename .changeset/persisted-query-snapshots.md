---
"@tanstack/query-core": minor
"@tanstack/query-persist-client-core": minor
---

Preserve the full cached query snapshot when restoring fine-grained persisted queries. `createPersisterRestoreResult` lets a persister return stored state — including errors, failure counts, timestamps, invalidation, and infinite-query page params — instead of recording a fresh successful fetch. Bulk restore merges data freshness and error freshness independently.
