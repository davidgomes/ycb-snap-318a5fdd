import {
  type FrameBuilderCallback,
  type OverBuilderCallback,
  sql,
} from '../../../'

import {
  BuiltInDialect,
  clearDatabase,
  Database,
  destroyTest,
  DIALECTS,
  expect,
  initTest,
  insertDefaultDataSet,
  NOT_SUPPORTED,
  PerDialect,
  TestContext,
  testSql,
} from './test-setup.js'

type OverCallback = OverBuilderCallback<Database, 'person'>

interface FrameCase {
  frame: FrameBuilderCallback
  mode: 'rows' | 'range' | 'groups'
  expectedFrame: string
  parameters: unknown[]
  // Counts for Arnold, Jennifer and Sylvester, ordered by first name.
  expectedCounts?: [number, number, number]
  executeOn?: BuiltInDialect[]
}

const ALL: BuiltInDialect[] = ['postgres', 'mysql', 'mssql', 'sqlite']
// Parameterized frame offsets aren't supported by MS SQL Server.
const PARAMETERIZED_OFFSETS: BuiltInDialect[] = ['postgres', 'mysql', 'sqlite']
// `groups` mode and frame exclusions aren't supported by MySQL or MS SQL Server.
const GROUPS_AND_EXCLUSIONS: BuiltInDialect[] = ['postgres', 'sqlite']

const FRAME_CASES: FrameCase[] = [
  {
    mode: 'rows',
    frame: (f) => f.unboundedPreceding(),
    expectedFrame: 'rows unbounded preceding',
    parameters: [],
    expectedCounts: [1, 2, 3],
    executeOn: ALL,
  },
  {
    mode: 'rows',
    frame: (f) => f.preceding(1),
    expectedFrame: 'rows $1 preceding',
    parameters: [1],
    expectedCounts: [1, 2, 2],
    executeOn: PARAMETERIZED_OFFSETS,
  },
  {
    mode: 'rows',
    frame: (f) => f.currentRow(),
    expectedFrame: 'rows current row',
    parameters: [],
    expectedCounts: [1, 1, 1],
    executeOn: ALL,
  },
  {
    mode: 'rows',
    frame: (f) => f.following(1),
    expectedFrame: 'rows $1 following',
    parameters: [1],
  },
  {
    mode: 'rows',
    frame: (f) => f.unboundedFollowing(),
    expectedFrame: 'rows unbounded following',
    parameters: [],
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenUnboundedPreceding().andUnboundedFollowing(),
    expectedFrame: 'rows between unbounded preceding and unbounded following',
    parameters: [],
    expectedCounts: [3, 3, 3],
    executeOn: ALL,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenUnboundedPreceding().andPreceding(1),
    expectedFrame: 'rows between unbounded preceding and $1 preceding',
    parameters: [1],
    expectedCounts: [0, 1, 2],
    executeOn: PARAMETERIZED_OFFSETS,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenUnboundedPreceding().andUnboundedPreceding(),
    expectedFrame: 'rows between unbounded preceding and unbounded preceding',
    parameters: [],
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenPreceding(1).andFollowing(1),
    expectedFrame: 'rows between $1 preceding and $2 following',
    parameters: [1, 1],
    expectedCounts: [2, 3, 2],
    executeOn: PARAMETERIZED_OFFSETS,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenPreceding(BigInt(2)).andCurrentRow(),
    expectedFrame: 'rows between $1 preceding and current row',
    parameters: [BigInt(2)],
    expectedCounts: [1, 2, 3],
    executeOn: PARAMETERIZED_OFFSETS,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenCurrentRow().andUnboundedFollowing(),
    expectedFrame: 'rows between current row and unbounded following',
    parameters: [],
    expectedCounts: [3, 2, 1],
    executeOn: ALL,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenFollowing(1).andFollowing(2),
    expectedFrame: 'rows between $1 following and $2 following',
    parameters: [1, 2],
    expectedCounts: [2, 1, 0],
    executeOn: PARAMETERIZED_OFFSETS,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenPreceding(sql.lit(1)).andFollowing(sql.lit(1)),
    expectedFrame: 'rows between 1 preceding and 1 following',
    parameters: [],
    expectedCounts: [2, 3, 2],
    executeOn: ALL,
  },
  {
    mode: 'range',
    frame: (f) => f.betweenCurrentRow().andUnboundedFollowing(),
    expectedFrame: 'range between current row and unbounded following',
    parameters: [],
    expectedCounts: [3, 2, 1],
    executeOn: ALL,
  },
  {
    mode: 'groups',
    frame: (f) => f.betweenCurrentRow().andFollowing(1),
    expectedFrame: 'groups between current row and $1 following',
    parameters: [1],
    expectedCounts: [2, 2, 1],
    executeOn: GROUPS_AND_EXCLUSIONS,
  },
  {
    mode: 'rows',
    frame: (f) =>
      f.betweenUnboundedPreceding().andUnboundedFollowing().excludeCurrentRow(),
    expectedFrame:
      'rows between unbounded preceding and unbounded following exclude current row',
    parameters: [],
    expectedCounts: [2, 2, 2],
    executeOn: GROUPS_AND_EXCLUSIONS,
  },
  {
    mode: 'range',
    frame: (f) =>
      f.betweenUnboundedPreceding().andUnboundedFollowing().excludeGroup(),
    expectedFrame:
      'range between unbounded preceding and unbounded following exclude group',
    parameters: [],
    expectedCounts: [2, 2, 2],
    executeOn: GROUPS_AND_EXCLUSIONS,
  },
  {
    mode: 'groups',
    frame: (f) => f.unboundedPreceding().excludeTies(),
    expectedFrame: 'groups unbounded preceding exclude ties',
    parameters: [],
    expectedCounts: [1, 2, 3],
    executeOn: GROUPS_AND_EXCLUSIONS,
  },
  {
    mode: 'rows',
    frame: (f) => f.betweenPreceding(1).andCurrentRow().excludeNoOthers(),
    expectedFrame:
      'rows between $1 preceding and current row exclude no others',
    parameters: [1],
    expectedCounts: [1, 2, 2],
    executeOn: GROUPS_AND_EXCLUSIONS,
  },
]

for (const dialect of DIALECTS) {
  describe(`${dialect}: window functions`, () => {
    let ctx: TestContext

    before(async function () {
      ctx = await initTest(this, dialect)
    })

    beforeEach(async () => {
      await insertDefaultDataSet(ctx)
    })

    afterEach(async () => {
      await clearDatabase(ctx)
    })

    after(async () => {
      await destroyTest(ctx)
    })

    describe('frames', () => {
      for (const frameCase of FRAME_CASES) {
        it(`should compile over(order by ... ${frameCase.expectedFrame})`, async () => {
          const query = ctx.db
            .selectFrom('person')
            .select((eb) => [
              'first_name',
              eb.fn
                .count<number | string>('id')
                .over((ob) =>
                  ob.orderBy('first_name')[frameCase.mode](frameCase.frame),
                )
                .as('frame_count'),
            ])
            .orderBy('first_name')

          testSql(
            query,
            dialect,
            perDialect(
              [
                'select "first_name",',
                `count("id") over(order by "first_name" ${frameCase.expectedFrame}) as "frame_count"`,
                'from "person"',
                'order by "first_name"',
              ],
              frameCase.parameters,
            ),
          )

          if (frameCase.executeOn?.includes(dialect)) {
            const result = await query.execute()

            expect(
              result.map((row) => [row.first_name, Number(row.frame_count)]),
            ).to.eql([
              ['Arnold', frameCase.expectedCounts![0]],
              ['Jennifer', frameCase.expectedCounts![1]],
              ['Sylvester', frameCase.expectedCounts![2]],
            ])
          }
        })
      }

      it('should include peer rows in range frames', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'first_name',
            eb.fn
              .count<number | string>('id')
              .over((ob) =>
                ob
                  .orderBy('gender')
                  .range((f) => f.betweenUnboundedPreceding().andCurrentRow()),
              )
              .as('range_count'),
            eb.fn
              .count<number | string>('id')
              .over((ob) =>
                ob
                  .orderBy('gender')
                  .orderBy('first_name')
                  .rows((f) => f.betweenUnboundedPreceding().andCurrentRow()),
              )
              .as('rows_count'),
          ])
          .orderBy('first_name')

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select "first_name",',
              'count("id") over(order by "gender" range between unbounded preceding and current row) as "range_count",',
              'count("id") over(order by "gender", "first_name" rows between unbounded preceding and current row) as "rows_count"',
              'from "person"',
              'order by "first_name"',
            ],
            [],
          ),
        )

        const result = await query.execute()

        expect(
          result.map((row) => [
            row.first_name,
            Number(row.range_count),
            Number(row.rows_count),
          ]),
        ).to.eql([
          ['Arnold', 3, 2],
          ['Jennifer', 1, 1],
          ['Sylvester', 3, 3],
        ])
      })

      if (PARAMETERIZED_OFFSETS.includes(dialect)) {
        it('should use value offsets in range frames', async () => {
          const query = ctx.db.selectFrom('person').select((eb) =>
            eb.fn
              .count<number | string>('id')
              .over((ob) =>
                ob
                  .orderBy('children')
                  .range((f) => f.betweenPreceding(1).andFollowing(1)),
              )
              .as('range_count'),
          )

          testSql(query, dialect, {
            ...perDialect(
              [
                'select count("id") over(order by "children" range between $1 preceding and $2 following) as "range_count"',
                'from "person"',
              ],
              [1, 1],
            ),
            mssql: NOT_SUPPORTED,
          })

          const result = await query.execute()

          expect(result.map((row) => Number(row.range_count))).to.eql([3, 3, 3])
        })
      }

      it('should add a frame after partition by and order by', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'first_name',
            eb.fn
              .count<number | string>('id')
              .over((ob) =>
                ob
                  .rows((f) => f.betweenUnboundedPreceding().andCurrentRow())
                  .orderBy('first_name')
                  .partitionBy('gender'),
              )
              .as('frame_count'),
          ])
          .orderBy('first_name')

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select "first_name",',
              'count("id") over(partition by "gender" order by "first_name" rows between unbounded preceding and current row) as "frame_count"',
              'from "person"',
              'order by "first_name"',
            ],
            [],
          ),
        )

        const result = await query.execute()

        expect(
          result.map((row) => [row.first_name, Number(row.frame_count)]),
        ).to.eql([
          ['Arnold', 1],
          ['Jennifer', 1],
          ['Sylvester', 2],
        ])
      })

      it('should add a frame without partition by or order by', async () => {
        const query = ctx.db.selectFrom('person').select((eb) =>
          eb.fn
            .count<number | string>('id')
            .over((ob) =>
              ob.rows((f) =>
                f.betweenUnboundedPreceding().andUnboundedFollowing(),
              ),
            )
            .as('frame_count'),
        )

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select count("id") over(rows between unbounded preceding and unbounded following) as "frame_count"',
              'from "person"',
            ],
            [],
          ),
        )

        // MS SQL Server requires an order by with a frame.
        if (dialect !== 'mssql') {
          const result = await query.execute()

          expect(result.map((row) => Number(row.frame_count))).to.eql([3, 3, 3])
        }
      })

      it('should replace the frame when called again', async () => {
        const query = ctx.db.selectFrom('person').select((eb) =>
          eb.fn
            .count<number | string>('id')
            .over((ob) =>
              ob
                .orderBy('first_name')
                .groups((f) => f.betweenPreceding(1).andFollowing(1))
                .rows((f) => f.betweenCurrentRow().andFollowing(sql.lit(1)))
                .range((f) => f.unboundedPreceding()),
            )
            .as('frame_count'),
        )

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select count("id") over(order by "first_name" range unbounded preceding) as "frame_count"',
              'from "person"',
            ],
            [],
          ),
        )

        await query.execute()
      })

      it('should interleave frame offset parameters with other parameters', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .count<number | string>('id')
              .filterWhere('gender', '=', 'male')
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .rows((f) => f.betweenPreceding(2).andFollowing(3)),
              )
              .as('frame_count'),
          )
          .where('first_name', '!=', 'Nobody')

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select count("id") filter(where "gender" = $1) over(order by "first_name" rows between $2 preceding and $3 following) as "frame_count"',
              'from "person"',
              'where "first_name" != $4',
            ],
            ['male', 2, 3, 'Nobody'],
          ),
        )

        // `filter` isn't supported by MySQL or MS SQL Server.
        if (dialect === 'postgres' || dialect === 'sqlite') {
          await query.execute()
        }
      })
    })

    describe('ranking functions', () => {
      it('should execute ranking functions', async () => {
        const byGender: OverCallback = (ob) => ob.orderBy('gender')
        const byGenderAndName: OverCallback = (ob) =>
          ob.orderBy('gender').orderBy('first_name')

        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'first_name',
            eb.fn.rowNumber().over(byGenderAndName).as('row_number'),
            eb.fn.rank().over(byGender).as('rank'),
            eb.fn.denseRank().over(byGender).as('dense_rank'),
            eb.fn.percentRank().over(byGender).as('percent_rank'),
            eb.fn.cumeDist().over(byGender).as('cume_dist'),
            eb.fn.ntile(2).over(byGenderAndName).as('ntile'),
          ])
          .orderBy('first_name')

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select "first_name",',
              'row_number() over(order by "gender", "first_name") as "row_number",',
              'rank() over(order by "gender") as "rank",',
              'dense_rank() over(order by "gender") as "dense_rank",',
              'percent_rank() over(order by "gender") as "percent_rank",',
              'cume_dist() over(order by "gender") as "cume_dist",',
              'ntile($1) over(order by "gender", "first_name") as "ntile"',
              'from "person"',
              'order by "first_name"',
            ],
            [2],
          ),
        )

        const result = await query.execute()

        expect(
          result.map((row) => ({
            first_name: row.first_name,
            row_number: Number(row.row_number),
            rank: Number(row.rank),
            dense_rank: Number(row.dense_rank),
            percent_rank: round(row.percent_rank),
            cume_dist: round(row.cume_dist),
            ntile: Number(row.ntile),
          })),
        ).to.eql([
          {
            first_name: 'Arnold',
            row_number: 2,
            rank: 2,
            dense_rank: 2,
            percent_rank: 0.5,
            cume_dist: 1,
            ntile: 1,
          },
          {
            first_name: 'Jennifer',
            row_number: 1,
            rank: 1,
            dense_rank: 1,
            percent_rank: 0,
            cume_dist: 0.333,
            ntile: 1,
          },
          {
            first_name: 'Sylvester',
            row_number: 3,
            rank: 2,
            dense_rank: 2,
            percent_rank: 0.5,
            cume_dist: 1,
            ntile: 2,
          },
        ])
      })
    })

    describe('value functions', () => {
      it('should execute first_value, last_value, lag and lead', async () => {
        const byName: OverCallback = (ob) => ob.orderBy('first_name')

        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'first_name',
            eb.fn.firstValue('first_name').over(byName).as('first'),
            eb.fn
              .lastValue('first_name')
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .rows((f) =>
                    f.betweenUnboundedPreceding().andUnboundedFollowing(),
                  ),
              )
              .as('last'),
            eb.fn.lag('first_name').over(byName).as('previous'),
            eb.fn.lead('first_name').over(byName).as('next'),
            eb.fn.lag('children', 2, 99).over(byName).as('lag_children'),
            eb.fn
              .lead('children', undefined, BigInt(-1))
              .over(byName)
              .as('lead_children'),
          ])
          .orderBy('first_name')

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select "first_name",',
              'first_value("first_name") over(order by "first_name") as "first",',
              'last_value("first_name") over(order by "first_name" rows between unbounded preceding and unbounded following) as "last",',
              'lag("first_name") over(order by "first_name") as "previous",',
              'lead("first_name") over(order by "first_name") as "next",',
              'lag("children", $1, $2) over(order by "first_name") as "lag_children",',
              'lead("children", $3, $4) over(order by "first_name") as "lead_children"',
              'from "person"',
              'order by "first_name"',
            ],
            [2, 99, 1, BigInt(-1)],
          ),
        )

        const result = await query.execute()

        expect(
          result.map((row) => ({
            ...row,
            lag_children: Number(row.lag_children),
            lead_children: Number(row.lead_children),
          })),
        ).to.eql([
          {
            first_name: 'Arnold',
            first: 'Arnold',
            last: 'Sylvester',
            previous: null,
            next: 'Jennifer',
            lag_children: 99,
            lead_children: 0,
          },
          {
            first_name: 'Jennifer',
            first: 'Arnold',
            last: 'Sylvester',
            previous: 'Arnold',
            next: 'Sylvester',
            lag_children: 99,
            lead_children: 0,
          },
          {
            first_name: 'Sylvester',
            first: 'Arnold',
            last: 'Sylvester',
            previous: 'Jennifer',
            next: null,
            lag_children: 0,
            lead_children: -1,
          },
        ])
      })

      if (dialect !== 'mssql') {
        it('should execute nth_value', async () => {
          const query = ctx.db
            .selectFrom('person')
            .select((eb) => [
              'first_name',
              eb.fn
                .nthValue('first_name', 2)
                .over((ob) => ob.orderBy('first_name'))
                .as('second'),
            ])
            .orderBy('first_name')

          testSql(query, dialect, {
            ...perDialect(
              [
                'select "first_name",',
                'nth_value("first_name", $1) over(order by "first_name") as "second"',
                'from "person"',
                'order by "first_name"',
              ],
              [2],
            ),
            mssql: NOT_SUPPORTED,
          })

          const result = await query.execute()

          expect(result).to.eql([
            { first_name: 'Arnold', second: null },
            { first_name: 'Jennifer', second: 'Jennifer' },
            { first_name: 'Sylvester', second: 'Jennifer' },
          ])
        })
      }

      it('should add null treatment right after the function arguments', async () => {
        const query = ctx.db.selectFrom('person').select((eb) => [
          eb.fn
            .firstValue('middle_name')
            .over((ob) => ob.orderBy('first_name'))
            .ignoreNulls()
            .as('first_middle_name'),
          eb.fn
            .lag('middle_name', 1)
            .respectNulls()
            .over((ob) => ob.orderBy('first_name'))
            .as('previous_middle_name'),
          eb.fn
            .agg<string>('last_value', ['middle_name'])
            .filterWhere('gender', '=', 'male')
            .ignoreNulls()
            .over()
            .as('last_middle_name'),
        ])

        testSql(
          query,
          dialect,
          perDialect(
            [
              'select first_value("middle_name") ignore nulls over(order by "first_name") as "first_middle_name",',
              'lag("middle_name", $1) respect nulls over(order by "first_name") as "previous_middle_name",',
              'last_value("middle_name") ignore nulls filter(where "gender" = $2) over() as "last_middle_name"',
              'from "person"',
            ],
            [1, 'male'],
          ),
        )
      })

      if (dialect === 'mysql' || dialect === 'mssql') {
        it('should execute respect nulls', async () => {
          const query = ctx.db.selectFrom('person').select((eb) =>
            eb.fn
              .firstValue('middle_name')
              .respectNulls()
              .over((ob) => ob.orderBy('first_name'))
              .as('first_middle_name'),
          )

          testSql(query, dialect, {
            ...perDialect(
              [
                'select first_value("middle_name") respect nulls over(order by "first_name") as "first_middle_name"',
                'from "person"',
              ],
              [],
            ),
            postgres: NOT_SUPPORTED,
            sqlite: NOT_SUPPORTED,
          })

          const result = await query.execute()

          expect(result).to.eql([
            { first_middle_name: null },
            { first_middle_name: null },
            { first_middle_name: null },
          ])
        })
      }

      if (dialect === 'mssql') {
        it('should execute ignore nulls', async () => {
          await ctx.db
            .updateTable('person')
            .set({ middle_name: 'Alois' })
            .where('first_name', '=', 'Arnold')
            .execute()

          const query = ctx.db.selectFrom('person').select((eb) =>
            eb.fn
              .lastValue('middle_name')
              .ignoreNulls()
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .rows((f) =>
                    f.betweenUnboundedPreceding().andUnboundedFollowing(),
                  ),
              )
              .as('last_middle_name'),
          )

          testSql(query, dialect, {
            postgres: NOT_SUPPORTED,
            mysql: NOT_SUPPORTED,
            mssql: {
              sql: [
                'select last_value("middle_name") ignore nulls over(order by "first_name" rows between unbounded preceding and unbounded following) as "last_middle_name"',
                'from "person"',
              ],
              parameters: [],
            },
            sqlite: NOT_SUPPORTED,
          })

          const result = await query.execute()

          expect(result).to.eql([
            { last_middle_name: 'Alois' },
            { last_middle_name: 'Alois' },
            { last_middle_name: 'Alois' },
          ])
        })
      }
    })
  })
}

/**
 * Builds the expected SQL for all dialects from PostgreSQL flavored SQL.
 */
function perDialect(
  postgresSql: string[],
  parameters: unknown[],
): PerDialect<{ sql: string[]; parameters: unknown[] }> {
  const convert = (
    identifierWrapper: string,
    placeholder: (index: string) => string,
  ) =>
    postgresSql.map((line) =>
      line
        .replace(/"/g, identifierWrapper)
        .replace(/\$(\d+)/g, (_, index) => placeholder(index)),
    )

  return {
    postgres: { sql: postgresSql, parameters },
    mysql: { sql: convert('`', () => '?'), parameters },
    mssql: { sql: convert('"', (index) => `@${index}`), parameters },
    sqlite: { sql: convert('"', () => '?'), parameters },
  }
}

function round(value: unknown): number {
  return Math.round(Number(value) * 1000) / 1000
}
