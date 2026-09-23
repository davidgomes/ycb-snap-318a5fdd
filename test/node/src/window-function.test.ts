import { sql } from '../../../'

import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  expect,
  insertDefaultDataSet,
  NOT_SUPPORTED,
  DIALECTS,
} from './test-setup.js'

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

    it('should execute ranking window functions', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'first_name',
          eb.fn
            .rowNumber()
            .over((ob) => ob.orderBy('first_name'))
            .as('row_number'),
          eb.fn
            .rank()
            .over((ob) => ob.orderBy('first_name'))
            .as('rank'),
          eb.fn
            .denseRank()
            .over((ob) => ob.orderBy('first_name'))
            .as('dense_rank'),
          eb.fn
            .percentRank()
            .over((ob) => ob.orderBy('first_name'))
            .as('percent_rank'),
          eb.fn
            .cumeDist()
            .over((ob) => ob.orderBy('first_name'))
            .as('cume_dist'),
          eb.fn
            .ntile(2)
            .over((ob) => ob.orderBy('first_name'))
            .as('ntile'),
        ])
        .orderBy('first_name')

      const over = 'over(order by "first_name")'
      const mysqlOver = 'over(order by `first_name`)'

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select "first_name",',
            `row_number() ${over} as "row_number",`,
            `rank() ${over} as "rank",`,
            `dense_rank() ${over} as "dense_rank",`,
            `percent_rank() ${over} as "percent_rank",`,
            `cume_dist() ${over} as "cume_dist",`,
            `ntile($1) ${over} as "ntile"`,
            'from "person" order by "first_name"',
          ],
          parameters: [2],
        },
        mysql: {
          sql: [
            'select `first_name`,',
            `row_number() ${mysqlOver} as \`row_number\`,`,
            `rank() ${mysqlOver} as \`rank\`,`,
            `dense_rank() ${mysqlOver} as \`dense_rank\`,`,
            `percent_rank() ${mysqlOver} as \`percent_rank\`,`,
            `cume_dist() ${mysqlOver} as \`cume_dist\`,`,
            `ntile(?) ${mysqlOver} as \`ntile\``,
            'from `person` order by `first_name`',
          ],
          parameters: [2],
        },
        mssql: {
          sql: [
            'select "first_name",',
            `row_number() ${over} as "row_number",`,
            `rank() ${over} as "rank",`,
            `dense_rank() ${over} as "dense_rank",`,
            `percent_rank() ${over} as "percent_rank",`,
            `cume_dist() ${over} as "cume_dist",`,
            `ntile(@1) ${over} as "ntile"`,
            'from "person" order by "first_name"',
          ],
          parameters: [2],
        },
        sqlite: {
          sql: [
            'select "first_name",',
            `row_number() ${over} as "row_number",`,
            `rank() ${over} as "rank",`,
            `dense_rank() ${over} as "dense_rank",`,
            `percent_rank() ${over} as "percent_rank",`,
            `cume_dist() ${over} as "cume_dist",`,
            `ntile(?) ${over} as "ntile"`,
            'from "person" order by "first_name"',
          ],
          parameters: [2],
        },
      })

      const result = await query.execute()

      expect(
        result.map((row) => ({
          first_name: row.first_name,
          row_number: Number(row.row_number),
          rank: Number(row.rank),
          dense_rank: Number(row.dense_rank),
          percent_rank: Number(row.percent_rank),
          cume_dist: Number(Number(row.cume_dist).toFixed(4)),
          ntile: Number(row.ntile),
        })),
      ).to.eql([
        {
          first_name: 'Arnold',
          row_number: 1,
          rank: 1,
          dense_rank: 1,
          percent_rank: 0,
          cume_dist: 0.3333,
          ntile: 1,
        },
        {
          first_name: 'Jennifer',
          row_number: 2,
          rank: 2,
          dense_rank: 2,
          percent_rank: 0.5,
          cume_dist: 0.6667,
          ntile: 1,
        },
        {
          first_name: 'Sylvester',
          row_number: 3,
          rank: 3,
          dense_rank: 3,
          percent_rank: 1,
          cume_dist: 1,
          ntile: 2,
        },
      ])
    })

    it('should execute value window functions', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'first_name',
          eb.fn
            .lag('first_name')
            .over((ob) => ob.orderBy('first_name'))
            .as('lag'),
          eb.fn
            .lead('children', 1, 100)
            .over((ob) => ob.orderBy('first_name'))
            .as('lead'),
          eb.fn
            .firstValue('first_name')
            .over((ob) => ob.orderBy('first_name'))
            .as('first_value'),
          eb.fn
            .lastValue('first_name')
            .over((ob) =>
              ob
                .orderBy('first_name')
                .rows((fb) =>
                  fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                ),
            )
            .as('last_value'),
        ])
        .orderBy('first_name')

      const over = 'over(order by "first_name")'
      const mysqlOver = 'over(order by `first_name`)'
      const frame = 'rows between unbounded preceding and unbounded following'

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select "first_name",',
            `lag("first_name") ${over} as "lag",`,
            `lead("children", $1, $2) ${over} as "lead",`,
            `first_value("first_name") ${over} as "first_value",`,
            `last_value("first_name") over(order by "first_name" ${frame}) as "last_value"`,
            'from "person" order by "first_name"',
          ],
          parameters: [1, 100],
        },
        mysql: {
          sql: [
            'select `first_name`,',
            `lag(\`first_name\`) ${mysqlOver} as \`lag\`,`,
            `lead(\`children\`, ?, ?) ${mysqlOver} as \`lead\`,`,
            `first_value(\`first_name\`) ${mysqlOver} as \`first_value\`,`,
            `last_value(\`first_name\`) over(order by \`first_name\` ${frame}) as \`last_value\``,
            'from `person` order by `first_name`',
          ],
          parameters: [1, 100],
        },
        mssql: {
          sql: [
            'select "first_name",',
            `lag("first_name") ${over} as "lag",`,
            `lead("children", @1, @2) ${over} as "lead",`,
            `first_value("first_name") ${over} as "first_value",`,
            `last_value("first_name") over(order by "first_name" ${frame}) as "last_value"`,
            'from "person" order by "first_name"',
          ],
          parameters: [1, 100],
        },
        sqlite: {
          sql: [
            'select "first_name",',
            `lag("first_name") ${over} as "lag",`,
            `lead("children", ?, ?) ${over} as "lead",`,
            `first_value("first_name") ${over} as "first_value",`,
            `last_value("first_name") over(order by "first_name" ${frame}) as "last_value"`,
            'from "person" order by "first_name"',
          ],
          parameters: [1, 100],
        },
      })

      const result = await query.execute()

      expect(result.map((row) => ({ ...row, lead: Number(row.lead) }))).to.eql([
        {
          first_name: 'Arnold',
          lag: null,
          lead: 0,
          first_value: 'Arnold',
          last_value: 'Sylvester',
        },
        {
          first_name: 'Jennifer',
          lag: 'Arnold',
          lead: 0,
          first_value: 'Arnold',
          last_value: 'Sylvester',
        },
        {
          first_name: 'Sylvester',
          lag: 'Jennifer',
          lead: 100,
          first_value: 'Arnold',
          last_value: 'Sylvester',
        },
      ])
    })

    if (dialect !== 'mssql') {
      it('should execute nth_value', async () => {
        const query = ctx.db.selectFrom('person').select((eb) =>
          eb.fn
            .nthValue('first_name', 2)
            .over((ob) =>
              ob
                .orderBy('first_name')
                .rows((fb) =>
                  fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                ),
            )
            .as('second'),
        )

        testSql(query, dialect, {
          postgres: {
            sql: 'select nth_value("first_name", $1) over(order by "first_name" rows between unbounded preceding and unbounded following) as "second" from "person"',
            parameters: [2],
          },
          mysql: {
            sql: 'select nth_value(`first_name`, ?) over(order by `first_name` rows between unbounded preceding and unbounded following) as `second` from `person`',
            parameters: [2],
          },
          mssql: NOT_SUPPORTED,
          sqlite: {
            sql: 'select nth_value("first_name", ?) over(order by "first_name" rows between unbounded preceding and unbounded following) as "second" from "person"',
            parameters: [2],
          },
        })

        const result = await query.execute()

        expect(result).to.eql([
          { second: 'Jennifer' },
          { second: 'Jennifer' },
          { second: 'Jennifer' },
        ])
      })
    }

    it('should execute a rows frame with parameter and literal offsets', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'first_name',
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .partitionBy('gender')
                .orderBy('first_name')
                .rows((fb) => fb.betweenPreceding(1).andFollowing(sql.lit(1))),
            )
            .as('count'),
        ])
        .orderBy('first_name')

      testSql(query, dialect, {
        postgres: {
          sql: 'select "first_name", count("id") over(partition by "gender" order by "first_name" rows between $1 preceding and 1 following) as "count" from "person" order by "first_name"',
          parameters: [1],
        },
        mysql: {
          sql: 'select `first_name`, count(`id`) over(partition by `gender` order by `first_name` rows between ? preceding and 1 following) as `count` from `person` order by `first_name`',
          parameters: [1],
        },
        mssql: {
          sql: 'select "first_name", count("id") over(partition by "gender" order by "first_name" rows between @1 preceding and 1 following) as "count" from "person" order by "first_name"',
          parameters: [1],
        },
        sqlite: {
          sql: 'select "first_name", count("id") over(partition by "gender" order by "first_name" rows between ? preceding and 1 following) as "count" from "person" order by "first_name"',
          parameters: [1],
        },
      })

      const result = await query.execute()

      expect(
        result.map((row) => ({ ...row, count: Number(row.count) })),
      ).to.eql([
        { first_name: 'Arnold', count: 2 },
        { first_name: 'Jennifer', count: 1 },
        { first_name: 'Sylvester', count: 2 },
      ])
    })

    if (dialect === 'postgres' || dialect === 'sqlite') {
      it('should execute a groups frame with an exclusion', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            'first_name',
            eb.fn
              .count<number>('id')
              .over((ob) =>
                ob
                  .orderBy('gender')
                  .groups((fb) =>
                    fb.betweenCurrentRow().andCurrentRow().excludeCurrentRow(),
                  ),
              )
              .as('peer_count'),
          ])
          .orderBy('first_name')

        testSql(query, dialect, {
          postgres: {
            sql: 'select "first_name", count("id") over(order by "gender" groups between current row and current row exclude current row) as "peer_count" from "person" order by "first_name"',
            parameters: [],
          },
          mysql: NOT_SUPPORTED,
          mssql: NOT_SUPPORTED,
          sqlite: {
            sql: 'select "first_name", count("id") over(order by "gender" groups between current row and current row exclude current row) as "peer_count" from "person" order by "first_name"',
            parameters: [],
          },
        })

        const result = await query.execute()

        expect(
          result.map((row) => ({ ...row, peer_count: Number(row.peer_count) })),
        ).to.eql([
          { first_name: 'Arnold', peer_count: 1 },
          { first_name: 'Jennifer', peer_count: 0 },
          { first_name: 'Sylvester', peer_count: 1 },
        ])
      })
    }

    it('should compile all frame bounds', () => {
      const cases = [
        [(fb: any) => fb.unboundedPreceding(), 'unbounded preceding', []],
        [(fb: any) => fb.preceding(3), '? preceding', [3]],
        [(fb: any) => fb.preceding(3n), '? preceding', [3n]],
        [(fb: any) => fb.currentRow(), 'current row', []],
        [(fb: any) => fb.following(sql.lit(2)), '2 following', []],
        [(fb: any) => fb.unboundedFollowing(), 'unbounded following', []],
        [
          (fb: any) => fb.betweenUnboundedPreceding().andPreceding(1),
          'between unbounded preceding and ? preceding',
          [1],
        ],
        [
          (fb: any) => fb.betweenPreceding(2).andUnboundedPreceding(),
          'between ? preceding and unbounded preceding',
          [2],
        ],
        [
          (fb: any) => fb.betweenCurrentRow().andFollowing(4),
          'between current row and ? following',
          [4],
        ],
        [
          (fb: any) => fb.betweenFollowing(1).andUnboundedFollowing(),
          'between ? following and unbounded following',
          [1],
        ],
        [
          (fb: any) => fb.currentRow().excludeGroup(),
          'current row exclude group',
          [],
        ],
        [
          (fb: any) => fb.betweenPreceding(1).andCurrentRow().excludeTies(),
          'between ? preceding and current row exclude ties',
          [1],
        ],
        [
          (fb: any) =>
            fb
              .betweenUnboundedPreceding()
              .andUnboundedFollowing()
              .excludeTies()
              .excludeNoOthers(),
          'between unbounded preceding and unbounded following exclude no others',
          [],
        ],
      ] as const

      for (const [frame, expectedFrame, expectedParameters] of cases) {
        for (const mode of ['rows', 'range', 'groups'] as const) {
          const compiled = ctx.db
            .selectFrom('person')
            .select((eb) =>
              eb.fn
                .sum('children')
                .over((ob) => ob[mode](frame))
                .as('s'),
            )
            .compile()

          const placeholders = { postgres: 0, mssql: 0 }
          const expectedSql = `${mode} ${expectedFrame}`.replace(/\?/g, () =>
            dialect === 'postgres'
              ? `$${++placeholders.postgres}`
              : dialect === 'mssql'
                ? `@${++placeholders.mssql}`
                : '?',
          )

          expect(compiled.sql).to.contain(`over(${expectedSql})`)
          expect(compiled.parameters).to.eql(expectedParameters)
        }
      }
    })

    it('should replace an existing frame', () => {
      const compiled = ctx.db
        .selectFrom('person')
        .select((eb) =>
          eb.fn
            .sum('children')
            .over((ob) =>
              ob
                .rows((fb) => fb.unboundedPreceding())
                .range((fb) => fb.currentRow()),
            )
            .as('s'),
        )
        .compile()

      expect(compiled.sql).to.contain('over(range current row)')
    })

    it('should compile null treatment clauses', () => {
      const query = ctx.db.selectFrom('person').select((eb) => [
        eb.fn
          .lag('first_name', 2)
          .ignoreNulls()
          .over((ob) => ob.orderBy('id'))
          .as('lag'),
        eb.fn
          .firstValue('middle_name')
          .respectNulls()
          .over((ob) => ob.partitionBy('gender').orderBy('id'))
          .as('first_middle_name'),
        eb.fn
          .lastValue('middle_name')
          .ignoreNulls()
          .filterWhere('children', '>', 0)
          .over()
          .as('last_middle_name'),
      ])

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select lag("first_name", $1) ignore nulls over(order by "id") as "lag",',
            'first_value("middle_name") respect nulls over(partition by "gender" order by "id") as "first_middle_name",',
            'last_value("middle_name") ignore nulls filter(where "children" > $2) over() as "last_middle_name"',
            'from "person"',
          ],
          parameters: [2, 0],
        },
        mysql: {
          sql: [
            'select lag(`first_name`, ?) ignore nulls over(order by `id`) as `lag`,',
            'first_value(`middle_name`) respect nulls over(partition by `gender` order by `id`) as `first_middle_name`,',
            'last_value(`middle_name`) ignore nulls filter(where `children` > ?) over() as `last_middle_name`',
            'from `person`',
          ],
          parameters: [2, 0],
        },
        mssql: {
          sql: [
            'select lag("first_name", @1) ignore nulls over(order by "id") as "lag",',
            'first_value("middle_name") respect nulls over(partition by "gender" order by "id") as "first_middle_name",',
            'last_value("middle_name") ignore nulls filter(where "children" > @2) over() as "last_middle_name"',
            'from "person"',
          ],
          parameters: [2, 0],
        },
        sqlite: {
          sql: [
            'select lag("first_name", ?) ignore nulls over(order by "id") as "lag",',
            'first_value("middle_name") respect nulls over(partition by "gender" order by "id") as "first_middle_name",',
            'last_value("middle_name") ignore nulls filter(where "children" > ?) over() as "last_middle_name"',
            'from "person"',
          ],
          parameters: [2, 0],
        },
      })
    })
  })
}
