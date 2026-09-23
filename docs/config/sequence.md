---
title: sequence | Config
outline: deep
---

# sequence

- **Type**: `{ sequencer?, shuffle?, seed?, hooks?, setupFiles?, groupOrder?, shardStrategy?, balanceShardsByTime?, recordFileDurations?, durationBasedSorting?, durationHistoryTTL?, durationHistoryPath?, durationHistoryMaxRuns?, durationSmoothing?, shardAffinityRules?, rebalanceThreshold?, isolateSlowThreshold?, durationFallbackStrategy? }`

Options for how tests should be sorted.

You can provide sequence options to CLI with dot notation:

```sh
npx vitest --sequence.shuffle --sequence.seed=1000
```

## sequence.sequencer <CRoot />

- **Type**: `TestSequencerConstructor`
- **Default**: `BaseSequencer`

A custom class that defines methods for sharding and sorting. You can extend `BaseSequencer` from `vitest/node`, if you only need to redefine one of the `sort` and `shard` methods, but both should exist.

Sharding is happening before sorting, and only if `--shard` option is provided.

If [`sequence.groupOrder`](#sequence-grouporder) is specified, the sequencer will be called once for each group and pool.

## sequence.groupOrder

- **Type:** `number`
- **Default:** `0`

Controls the order in which this project runs its tests when using multiple [projects](/guide/projects).

- Projects with the same group order number will run together, and groups are run from lowest to highest.
- If you don't set this option, all projects run in parallel.
- If several projects use the same group order, they will run at the same time.

This setting only affects the order in which projects run, not the order of tests within a project.
To control test isolation or the order of tests inside a project, use the [`isolate`](/config/isolate) and [`sequence.sequencer`](/config/sequence#sequence-sequencer) options.

::: details Example
Consider this example:

```ts
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    projects: [
      {
        test: {
          name: 'slow',
          sequence: {
            groupOrder: 0,
          },
        },
      },
      {
        test: {
          name: 'fast',
          sequence: {
            groupOrder: 0,
          },
        },
      },
      {
        test: {
          name: 'flaky',
          sequence: {
            groupOrder: 1,
          },
        },
      },
    ],
  },
})
```

Tests in these projects will run in this order:

```
 0. slow  |
          |> running together
 0. fast  |

 1. flaky |> runs after slow and fast alone
```
:::

## sequence.shuffle

- **Type**: `boolean | { files?, tests? }`
- **Default**: `false`
- **CLI**: `--sequence.shuffle`, `--sequence.shuffle=false`

If you want files and tests to run randomly, you can enable it with this option, or CLI argument [`--sequence.shuffle`](/guide/cli).

Vitest usually uses cache to sort tests, so long-running tests start earlier, which makes tests run faster. If your files and tests run in random order, you will lose this performance improvement, but it may be useful to track tests that accidentally depend on another test run previously.

### sequence.shuffle.files {#sequence-shuffle-files}

- **Type**: `boolean`
- **Default**: `false`
- **CLI**: `--sequence.shuffle.files`, `--sequence.shuffle.files=false`

Whether to randomize files, be aware that long running tests will not start earlier if you enable this option.

### sequence.shuffle.tests {#sequence-shuffle-tests}

- **Type**: `boolean`
- **Default**: `false`
- **CLI**: `--sequence.shuffle.tests`, `--sequence.shuffle.tests=false`

Whether to randomize tests.

## sequence.concurrent {#sequence-concurrent}

- **Type**: `boolean`
- **Default**: `false`
- **CLI**: `--sequence.concurrent`, `--sequence.concurrent=false`

If you want tests to run in parallel, you can enable it with this option, or CLI argument [`--sequence.concurrent`](/guide/cli).

::: warning
When you run tests with `sequence.concurrent` and `expect.requireAssertions` set to `true`, you should use [local expect](/guide/test-context.html#expect) instead of the global one. Otherwise, this may cause false negatives in [some situations (#8469)](https://github.com/vitest-dev/vitest/issues/8469).
:::

## sequence.seed <CRoot />

- **Type**: `number`
- **Default**: `Date.now()`
- **CLI**: `--sequence.seed=1000`

Sets the randomization seed, if tests are running in random order.

## sequence.hooks

- **Type**: `'stack' | 'list' | 'parallel'`
- **Default**: `'stack'`
- **CLI**: `--sequence.hooks=<value>`

Changes the order in which hooks are executed.

- `stack` will order "after" hooks in reverse order, "before" hooks will run in the order they were defined
- `list` will order all hooks in the order they are defined
- `parallel` runs hooks in a single group in parallel (hooks in parent suites still run before the current suite's hooks). The actual number of simultaneously running hooks is limited by [`maxConcurrency`](/config/maxconcurrency).

::: tip
This option doesn't affect [`onTestFinished`](/api/hooks#ontestfinished). It is always called in reverse order.
:::

## sequence.setupFiles {#sequence-setupfiles}

- **Type**: `'list' | 'parallel'`
- **Default**: `'parallel'`
- **CLI**: `--sequence.setupFiles=<value>`

Changes the order in which setup files are executed.

- `list` will run setup files in the order they are defined
- `parallel` will run setup files in parallel

## sequence.shardStrategy

- **Type**: `'hash' | 'time' | 'round-robin' | 'affinity'`
- **Default**: `'hash'`

How files are divided when [`--shard`](/guide/cli) is set.

- `hash` keeps the historical hash-based distribution
- `time` assigns the longest remaining file to the shard with the lowest total duration
- `round-robin` sorts by duration and deals files with a bouncing pointer, so the first and last shards receive two files in a row when the pointer turns around
- `affinity` pins files with [`sequence.shardAffinityRules`](#sequence-shardaffinityrules) and packs everything else by duration

If the duration history is missing or corrupt, Vitest uses [`sequence.durationFallbackStrategy`](#sequence-durationfallbackstrategy) instead.

## sequence.balanceShardsByTime

- **Type**: `boolean`
- **Default**: `false`

When `true` and `shardStrategy` is not set, Vitest resolves `shardStrategy` to `'time'`. If the resolved strategy is not `'time'`, this flag is forced to `false`.

## sequence.recordFileDurations

- **Type**: `boolean`
- **Default**: `false`

After a test run finishes, write each file's duration to [`sequence.durationHistoryPath`](#sequence-durationhistorypath). Durations are stored as integer milliseconds. Entries for files that did not run are left in place.

## sequence.durationBasedSorting

- **Type**: `boolean`
- **Default**: `false`

Sort files by recorded duration, longest first. Files that are absent from the history are ordered after files that have a record.

## sequence.durationHistoryPath

- **Type**: `string`
- **Default**: `'duration-history.json'`

Path to the duration history file, relative to the project root. The value must be a non-empty string without leading or trailing whitespace.

Keys are slash-normalized paths relative to the project root. A single stored run uses `{ "duration": number, "recordedAt": number }`. Several runs use `{ "observations": [...] }`. A legacy numeric value is read as one observation with `recordedAt: 0`.

## sequence.durationHistoryTTL

- **Type**: `number`
- **Default**: `0`

Drop observations whose `recordedAt` is older than `Date.now() - durationHistoryTTL`. `0` disables expiry. Observations recorded at `0` never expire.

## sequence.durationHistoryMaxRuns

- **Type**: `number`
- **Default**: `1`

Maximum number of observations written for each file. The newest observations are kept. One observation is written as `{ duration, recordedAt }`; more than one is written as `{ observations }`.

## sequence.durationSmoothing

- **Type**: `'latest' | 'average' | 'p95' | 'median'`
- **Default**: `'latest'`

How non-expired observations are reduced to a single duration. Files missing from the history use duration `0`.

## sequence.shardAffinityRules

- **Type**: `Array<{ pattern: string, shardIndex: number }>`
- **Default**: `[]`

Glob rules used when `shardStrategy` is `'affinity'`. The first matching pattern wins. `shardIndex` is zero-based and is clamped to `shardCount - 1`. If no rule matches any file, Vitest falls back to the `time` strategy.

## sequence.rebalanceThreshold

- **Type**: `number`
- **Default**: `0`

After sharding, warn when `minLoad / maxLoad` is lower than this value. The warning includes `ratio=` and `threshold=` formatted to two decimal places. `0` disables the warning.

## sequence.isolateSlowThreshold

- **Type**: `number`
- **Default**: `0`

`0` disables isolation. Otherwise, files whose recorded duration is strictly greater than this threshold are treated as slow. Each shard receives one slow file. When there are at least as many slow files as shards, the last shard also receives the extra slow files and every remaining file. When there are fewer slow files than shards, the remaining files are distributed with `shardStrategy`, and duration packing counts the slow files already assigned.

## sequence.durationFallbackStrategy

- **Type**: `'hash' | 'equal-split'`
- **Default**: `'hash'`

Used when the duration history cannot be read. `equal-split` sorts files by path and assigns index `i` to shard `(i % count) + 1`.
