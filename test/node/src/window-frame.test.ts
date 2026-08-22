import { sql } from '../../../'

import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  DIALECTS,
} from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: window frame`, () => {
    let ctx: TestContext

    before(async function () {
      ctx = await initTest(this, dialect)
    })

    beforeEach(async () => {
      await clearDatabase(ctx)
    })

    after(async () => {
      await destroyTest(ctx)
    })

    if (dialect === 'postgres' || dialect === 'mssql') {
      it('should compile rows between frame', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .rowNumber()
              .over((ob) =>
                ob
                  .partitionBy('gender')
                  .orderBy('first_name')
                  .rows((fb) =>
                    fb.betweenUnboundedPreceding().andCurrentRow(),
                  ),
              )
              .as('row_num'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select row_number() over(partition by "gender" order by "first_name" rows between unbounded preceding and current row) as "row_num" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select row_number() over(partition by "gender" order by "first_name" rows between unbounded preceding and current row) as "row_num" from "person"',
            parameters: [],
          },
        })
      })

      it('should compile range single-bound shorthand', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('id')
              .over((ob) =>
                ob.orderBy('first_name').range((fb) => fb.unboundedPreceding()),
              )
              .as('running_sum'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select sum("id") over(order by "first_name" range unbounded preceding) as "running_sum" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select sum("id") over(order by "first_name" range unbounded preceding) as "running_sum" from "person"',
            parameters: [],
          },
        })
      })

      it('should compile groups frame with exclusion', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .avg<number>('id')
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .groups((fb) =>
                    fb
                      .betweenCurrentRow()
                      .andUnboundedFollowing()
                      .excludeTies(),
                  ),
              )
              .as('avg_id'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select avg("id") over(order by "first_name" groups between current row and unbounded following exclude ties) as "avg_id" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select avg("id") over(order by "first_name" groups between current row and unbounded following exclude ties) as "avg_id" from "person"',
            parameters: [],
          },
        })
      })

      it('should compile parameterized frame offsets', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .rowNumber()
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .rows((fb) => fb.betweenPreceding(2).andFollowing(1)),
              )
              .as('row_num'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select row_number() over(order by "first_name" rows between $1 preceding and $2 following) as "row_num" from "person"',
            parameters: [2, 1],
          },
          mssql: {
            sql: 'select row_number() over(order by "first_name" rows between @1 preceding and @2 following) as "row_num" from "person"',
            parameters: [2, 1],
          },
        })
      })

      it('should compile ranking and value window functions', async () => {
        const query = ctx.db
          .selectFrom('person')
          .select((eb) => [
            eb.fn.rank().over((ob) => ob.orderBy('id')).as('rank'),
            eb.fn
              .firstValue('first_name')
              .respectNulls()
              .over((ob) => ob.orderBy('id'))
              .as('first_name'),
            eb.fn
              .lag('first_name', 1, 0)
              .ignoreNulls()
              .over((ob) => ob.orderBy('id'))
              .as('prev_name'),
            eb.fn.ntile(4).over((ob) => ob.orderBy('id')).as('quartile'),
          ])

        testSql(query, dialect, {
          postgres: {
            sql: [
              'select rank() over(order by "id") as "rank",',
              'first_value("first_name") respect nulls over(order by "id") as "first_name",',
              'lag("first_name", $1, $2) ignore nulls over(order by "id") as "prev_name",',
              'ntile($3) over(order by "id") as "quartile"',
              'from "person"',
            ],
            parameters: [1, 0, 4],
          },
          mssql: {
            sql: [
              'select rank() over(order by "id") as "rank",',
              'first_value("first_name") respect nulls over(order by "id") as "first_name",',
              'lag("first_name", @1, @2) ignore nulls over(order by "id") as "prev_name",',
              'ntile(@3) over(order by "id") as "quartile"',
              'from "person"',
            ],
            parameters: [1, 0, 4],
          },
        })
      })
    }
  })
}
