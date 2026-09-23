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

    it('should compile single bound frame shorthands', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob.orderBy('id').rows((fb) => fb.unboundedPreceding()),
            )
            .as('a'),
          eb.fn
            .count<number>('id')
            .over((ob) => ob.orderBy('id').rows((fb) => fb.preceding(1)))
            .as('b'),
          eb.fn
            .count<number>('id')
            .over((ob) => ob.orderBy('id').rows((fb) => fb.currentRow()))
            .as('c'),
        ])
        .orderBy('id')

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select',
            'count("id") over(order by "id" rows unbounded preceding) as "a",',
            'count("id") over(order by "id" rows $1 preceding) as "b",',
            'count("id") over(order by "id" rows current row) as "c"',
            'from "person" order by "id"',
          ],
          parameters: [1],
        },
        mysql: {
          sql: [
            'select',
            'count(`id`) over(order by `id` rows unbounded preceding) as `a`,',
            'count(`id`) over(order by `id` rows ? preceding) as `b`,',
            'count(`id`) over(order by `id` rows current row) as `c`',
            'from `person` order by `id`',
          ],
          parameters: [1],
        },
        mssql: {
          sql: [
            'select',
            'count("id") over(order by "id" rows unbounded preceding) as "a",',
            'count("id") over(order by "id" rows @1 preceding) as "b",',
            'count("id") over(order by "id" rows current row) as "c"',
            'from "person" order by "id"',
          ],
          parameters: [1],
        },
        sqlite: {
          sql: [
            'select',
            'count("id") over(order by "id" rows unbounded preceding) as "a",',
            'count("id") over(order by "id" rows ? preceding) as "b",',
            'count("id") over(order by "id" rows current row) as "c"',
            'from "person" order by "id"',
          ],
          parameters: [1],
        },
      })

      if (dialect === 'postgres' || dialect === 'sqlite') {
        const result = await query.execute()
        expect(result.map((it) => Number(it.a))).to.eql([1, 2, 3])
        expect(result.map((it) => Number(it.b))).to.eql([1, 2, 2])
        expect(result.map((it) => Number(it.c))).to.eql([1, 1, 1])
      }
    })

    it('should compile the remaining single bound shorthands', () => {
      const query = ctx.db.selectFrom('person').select((eb) => [
        eb.fn
          .count<number>('id')
          .over((ob) => ob.rows((fb) => fb.following(sql.lit(2))))
          .as('a'),
        eb.fn
          .count<number>('id')
          .over((ob) => ob.rows((fb) => fb.unboundedFollowing()))
          .as('b'),
      ])

      testSql(query, dialect, {
        postgres: {
          sql: 'select count("id") over(rows 2 following) as "a", count("id") over(rows unbounded following) as "b" from "person"',
          parameters: [],
        },
        mysql: {
          sql: 'select count(`id`) over(rows 2 following) as `a`, count(`id`) over(rows unbounded following) as `b` from `person`',
          parameters: [],
        },
        mssql: {
          sql: 'select count("id") over(rows 2 following) as "a", count("id") over(rows unbounded following) as "b" from "person"',
          parameters: [],
        },
        sqlite: {
          sql: 'select count("id") over(rows 2 following) as "a", count("id") over(rows unbounded following) as "b" from "person"',
          parameters: [],
        },
      })
    })

    it('should compile two-sided frames in every mode', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          eb.fn
            .sum<number>('id')
            .over((ob) =>
              ob
                .partitionBy('gender')
                .orderBy('id')
                .rows((fb) => fb.betweenPreceding(1).andFollowing(2)),
            )
            .as('a'),
          eb.fn
            .sum<number>('id')
            .over((ob) =>
              ob
                .orderBy('id')
                .range((fb) => fb.betweenCurrentRow().andUnboundedFollowing()),
            )
            .as('b'),
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .orderBy('gender')
                .groups((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
            )
            .as('c'),
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .orderBy('id')
                .rows((fb) =>
                  fb.betweenFollowing(sql.lit(1)).andUnboundedFollowing(),
                ),
            )
            .as('d'),
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .orderBy('id')
                .rows((fb) =>
                  fb.betweenUnboundedPreceding().andPreceding(sql.lit(1)),
                ),
            )
            .as('e'),
        ])
        .orderBy('id')

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select',
            'sum("id") over(partition by "gender" order by "id" rows between $1 preceding and $2 following) as "a",',
            'sum("id") over(order by "id" range between current row and unbounded following) as "b",',
            'count("id") over(order by "gender" groups between unbounded preceding and current row) as "c",',
            'count("id") over(order by "id" rows between 1 following and unbounded following) as "d",',
            'count("id") over(order by "id" rows between unbounded preceding and 1 preceding) as "e"',
            'from "person" order by "id"',
          ],
          parameters: [1, 2],
        },
        mysql: NOT_SUPPORTED,
        mssql: NOT_SUPPORTED,
        sqlite: {
          sql: [
            'select',
            'sum("id") over(partition by "gender" order by "id" rows between ? preceding and ? following) as "a",',
            'sum("id") over(order by "id" range between current row and unbounded following) as "b",',
            'count("id") over(order by "gender" groups between unbounded preceding and current row) as "c",',
            'count("id") over(order by "id" rows between 1 following and unbounded following) as "d",',
            'count("id") over(order by "id" rows between unbounded preceding and 1 preceding) as "e"',
            'from "person" order by "id"',
          ],
          parameters: [1, 2],
        },
      })

      if (dialect === 'postgres' || dialect === 'sqlite') {
        const result = await query.execute()
        expect(result.map((it) => Number(it.c))).to.eql([1, 3, 3])
        expect(result.map((it) => Number(it.d))).to.eql([2, 1, 0])
        expect(result.map((it) => Number(it.e))).to.eql([0, 1, 2])
      }
    })

    it('should compile exclusion modifiers', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .orderBy('id')
                .rows((fb) =>
                  fb
                    .betweenUnboundedPreceding()
                    .andUnboundedFollowing()
                    .excludeCurrentRow(),
                ),
            )
            .as('a'),
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .orderBy('gender')
                .range((fb) =>
                  fb
                    .betweenUnboundedPreceding()
                    .andUnboundedFollowing()
                    .excludeGroup(),
                ),
            )
            .as('b'),
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .orderBy('gender')
                .range((fb) =>
                  fb
                    .betweenUnboundedPreceding()
                    .andUnboundedFollowing()
                    .excludeTies(),
                ),
            )
            .as('c'),
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob.orderBy('id').rows((fb) => fb.currentRow().excludeNoOthers()),
            )
            .as('d'),
        ])
        .orderBy('id')

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select',
            'count("id") over(order by "id" rows between unbounded preceding and unbounded following exclude current row) as "a",',
            'count("id") over(order by "gender" range between unbounded preceding and unbounded following exclude group) as "b",',
            'count("id") over(order by "gender" range between unbounded preceding and unbounded following exclude ties) as "c",',
            'count("id") over(order by "id" rows current row exclude no others) as "d"',
            'from "person" order by "id"',
          ],
          parameters: [],
        },
        mysql: NOT_SUPPORTED,
        mssql: NOT_SUPPORTED,
        sqlite: {
          sql: [
            'select',
            'count("id") over(order by "id" rows between unbounded preceding and unbounded following exclude current row) as "a",',
            'count("id") over(order by "gender" range between unbounded preceding and unbounded following exclude group) as "b",',
            'count("id") over(order by "gender" range between unbounded preceding and unbounded following exclude ties) as "c",',
            'count("id") over(order by "id" rows current row exclude no others) as "d"',
            'from "person" order by "id"',
          ],
          parameters: [],
        },
      })

      if (dialect === 'postgres' || dialect === 'sqlite') {
        const result = await query.execute()
        expect(result.map((it) => Number(it.a))).to.eql([2, 2, 2])
        expect(result.map((it) => Number(it.b))).to.eql([2, 1, 1])
        expect(result.map((it) => Number(it.c))).to.eql([3, 2, 2])
        expect(result.map((it) => Number(it.d))).to.eql([1, 1, 1])
      }
    })

    it('should compile ranking functions', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          'first_name',
          eb.fn
            .rowNumber<number>()
            .over((ob) => ob.orderBy('id'))
            .as('row_number'),
          eb.fn
            .rank<number>()
            .over((ob) => ob.orderBy('gender'))
            .as('rank'),
          eb.fn
            .denseRank<number>()
            .over((ob) => ob.orderBy('gender'))
            .as('dense_rank'),
          eb.fn
            .percentRank<number>()
            .over((ob) => ob.orderBy('gender'))
            .as('percent_rank'),
          eb.fn
            .cumeDist<number>()
            .over((ob) => ob.orderBy('gender'))
            .as('cume_dist'),
          eb.fn
            .ntile<number>(2)
            .over((ob) => ob.orderBy('id'))
            .as('ntile'),
        ])
        .orderBy('id')

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select "first_name",',
            'row_number() over(order by "id") as "row_number",',
            'rank() over(order by "gender") as "rank",',
            'dense_rank() over(order by "gender") as "dense_rank",',
            'percent_rank() over(order by "gender") as "percent_rank",',
            'cume_dist() over(order by "gender") as "cume_dist",',
            'ntile($1) over(order by "id") as "ntile"',
            'from "person" order by "id"',
          ],
          parameters: [2],
        },
        mysql: {
          sql: [
            'select `first_name`,',
            'row_number() over(order by `id`) as `row_number`,',
            'rank() over(order by `gender`) as `rank`,',
            'dense_rank() over(order by `gender`) as `dense_rank`,',
            'percent_rank() over(order by `gender`) as `percent_rank`,',
            'cume_dist() over(order by `gender`) as `cume_dist`,',
            'ntile(?) over(order by `id`) as `ntile`',
            'from `person` order by `id`',
          ],
          parameters: [2],
        },
        mssql: {
          sql: [
            'select "first_name",',
            'row_number() over(order by "id") as "row_number",',
            'rank() over(order by "gender") as "rank",',
            'dense_rank() over(order by "gender") as "dense_rank",',
            'percent_rank() over(order by "gender") as "percent_rank",',
            'cume_dist() over(order by "gender") as "cume_dist",',
            'ntile(@1) over(order by "id") as "ntile"',
            'from "person" order by "id"',
          ],
          parameters: [2],
        },
        sqlite: {
          sql: [
            'select "first_name",',
            'row_number() over(order by "id") as "row_number",',
            'rank() over(order by "gender") as "rank",',
            'dense_rank() over(order by "gender") as "dense_rank",',
            'percent_rank() over(order by "gender") as "percent_rank",',
            'cume_dist() over(order by "gender") as "cume_dist",',
            'ntile(?) over(order by "id") as "ntile"',
            'from "person" order by "id"',
          ],
          parameters: [2],
        },
      })

      if (dialect === 'postgres' || dialect === 'sqlite') {
        const result = await query.execute()
        expect(
          result.map((it) => ({
            first_name: it.first_name,
            row_number: Number(it.row_number),
            rank: Number(it.rank),
            dense_rank: Number(it.dense_rank),
            percent_rank: Number(it.percent_rank),
            cume_dist: Number(it.cume_dist),
            ntile: Number(it.ntile),
          })),
        ).to.eql([
          {
            first_name: 'Jennifer',
            row_number: 1,
            rank: 1,
            dense_rank: 1,
            percent_rank: 0,
            cume_dist: 1 / 3,
            ntile: 1,
          },
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
            first_name: 'Sylvester',
            row_number: 3,
            rank: 2,
            dense_rank: 2,
            percent_rank: 0.5,
            cume_dist: 1,
            ntile: 2,
          },
        ])
      }
    })

    it('should compile value functions', async () => {
      const query = ctx.db
        .selectFrom('person')
        .select((eb) => [
          eb.fn
            .firstValue('first_name')
            .over((ob) => ob.orderBy('id'))
            .as('first'),
          eb.fn
            .lastValue('first_name')
            .over((ob) =>
              ob
                .orderBy('id')
                .rows((fb) =>
                  fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                ),
            )
            .as('last'),
          eb.fn
            .nthValue('first_name', 2)
            .over((ob) => ob.orderBy('id'))
            .as('second'),
          eb.fn
            .lag('first_name')
            .over((ob) => ob.orderBy('id'))
            .as('previous'),
          eb.fn
            .lead('id', 1, 0)
            .over((ob) => ob.orderBy('id'))
            .as('next_id'),
          eb.fn
            .lag('id', undefined, 0)
            .over((ob) => ob.orderBy('id'))
            .as('previous_id'),
        ])
        .orderBy('id')

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select',
            'first_value("first_name") over(order by "id") as "first",',
            'last_value("first_name") over(order by "id" rows between unbounded preceding and unbounded following) as "last",',
            'nth_value("first_name", $1) over(order by "id") as "second",',
            'lag("first_name") over(order by "id") as "previous",',
            'lead("id", $2, $3) over(order by "id") as "next_id",',
            'lag("id", $4, $5) over(order by "id") as "previous_id"',
            'from "person" order by "id"',
          ],
          parameters: [2, 1, 0, 1, 0],
        },
        mysql: {
          sql: [
            'select',
            'first_value(`first_name`) over(order by `id`) as `first`,',
            'last_value(`first_name`) over(order by `id` rows between unbounded preceding and unbounded following) as `last`,',
            'nth_value(`first_name`, ?) over(order by `id`) as `second`,',
            'lag(`first_name`) over(order by `id`) as `previous`,',
            'lead(`id`, ?, ?) over(order by `id`) as `next_id`,',
            'lag(`id`, ?, ?) over(order by `id`) as `previous_id`',
            'from `person` order by `id`',
          ],
          parameters: [2, 1, 0, 1, 0],
        },
        mssql: {
          sql: [
            'select',
            'first_value("first_name") over(order by "id") as "first",',
            'last_value("first_name") over(order by "id" rows between unbounded preceding and unbounded following) as "last",',
            'nth_value("first_name", @1) over(order by "id") as "second",',
            'lag("first_name") over(order by "id") as "previous",',
            'lead("id", @2, @3) over(order by "id") as "next_id",',
            'lag("id", @4, @5) over(order by "id") as "previous_id"',
            'from "person" order by "id"',
          ],
          parameters: [2, 1, 0, 1, 0],
        },
        sqlite: {
          sql: [
            'select',
            'first_value("first_name") over(order by "id") as "first",',
            'last_value("first_name") over(order by "id" rows between unbounded preceding and unbounded following) as "last",',
            'nth_value("first_name", ?) over(order by "id") as "second",',
            'lag("first_name") over(order by "id") as "previous",',
            'lead("id", ?, ?) over(order by "id") as "next_id",',
            'lag("id", ?, ?) over(order by "id") as "previous_id"',
            'from "person" order by "id"',
          ],
          parameters: [2, 1, 0, 1, 0],
        },
      })

      if (dialect === 'postgres' || dialect === 'sqlite') {
        const result = await query.execute()
        const ids = (
          await ctx.db.selectFrom('person').select('id').orderBy('id').execute()
        ).map((it) => it.id)

        expect(result).to.eql([
          {
            first: 'Jennifer',
            last: 'Sylvester',
            second: null,
            previous: null,
            next_id: ids[1],
            previous_id: 0,
          },
          {
            first: 'Jennifer',
            last: 'Sylvester',
            second: 'Arnold',
            previous: 'Jennifer',
            next_id: ids[2],
            previous_id: ids[0],
          },
          {
            first: 'Jennifer',
            last: 'Sylvester',
            second: 'Arnold',
            previous: 'Arnold',
            next_id: 0,
            previous_id: ids[1],
          },
        ])
      }
    })

    it('should compile null treatment after the function arguments', () => {
      const query = ctx.db.selectFrom('person').select((eb) => [
        eb.fn
          .lastValue('middle_name')
          .ignoreNulls()
          .over((ob) => ob.orderBy('id'))
          .as('a'),
        eb.fn
          .lag('middle_name', 2)
          .respectNulls()
          .filterWhere('gender', '=', 'male')
          .over()
          .as('b'),
      ])

      testSql(query, dialect, {
        postgres: {
          sql: [
            'select',
            'last_value("middle_name") ignore nulls over(order by "id") as "a",',
            'lag("middle_name", $1) respect nulls filter(where "gender" = $2) over() as "b"',
            'from "person"',
          ],
          parameters: [2, 'male'],
        },
        mysql: {
          sql: [
            'select',
            'last_value(`middle_name`) ignore nulls over(order by `id`) as `a`,',
            'lag(`middle_name`, ?) respect nulls filter(where `gender` = ?) over() as `b`',
            'from `person`',
          ],
          parameters: [2, 'male'],
        },
        mssql: {
          sql: [
            'select',
            'last_value("middle_name") ignore nulls over(order by "id") as "a",',
            'lag("middle_name", @1) respect nulls filter(where "gender" = @2) over() as "b"',
            'from "person"',
          ],
          parameters: [2, 'male'],
        },
        sqlite: {
          sql: [
            'select',
            'last_value("middle_name") ignore nulls over(order by "id") as "a",',
            'lag("middle_name", ?) respect nulls filter(where "gender" = ?) over() as "b"',
            'from "person"',
          ],
          parameters: [2, 'male'],
        },
      })
    })
  })
}
