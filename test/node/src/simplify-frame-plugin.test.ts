import { SimplifyFramePlugin } from '../../../'

import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  DIALECTS,
} from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: simplify frame plugin`, () => {
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
      it('should strip redundant default range frame with order by', async () => {
        const query = ctx.db
          .withPlugin(new SimplifyFramePlugin())
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('id')
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .range((fb) =>
                    fb.betweenUnboundedPreceding().andCurrentRow(),
                  ),
              )
              .as('running_sum'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select sum("id") over(order by "first_name") as "running_sum" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select sum("id") over(order by "first_name") as "running_sum" from "person"',
            parameters: [],
          },
        })
      })

      it('should strip redundant default range frame without order by', async () => {
        const query = ctx.db
          .withPlugin(new SimplifyFramePlugin())
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .avg<number>('id')
              .over((ob) =>
                ob.range((fb) =>
                  fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                ),
              )
              .as('avg_id'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select avg("id") over() as "avg_id" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select avg("id") over() as "avg_id" from "person"',
            parameters: [],
          },
        })
      })

      it('should preserve non-default rows frame', async () => {
        const query = ctx.db
          .withPlugin(new SimplifyFramePlugin())
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('id')
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .rows((fb) =>
                    fb.betweenUnboundedPreceding().andCurrentRow(),
                  ),
              )
              .as('running_sum'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select sum("id") over(order by "first_name" rows between unbounded preceding and current row) as "running_sum" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select sum("id") over(order by "first_name" rows between unbounded preceding and current row) as "running_sum" from "person"',
            parameters: [],
          },
        })
      })

      it('should preserve frame with exclusion clause', async () => {
        const query = ctx.db
          .withPlugin(new SimplifyFramePlugin())
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('id')
              .over((ob) =>
                ob
                  .orderBy('first_name')
                  .range((fb) =>
                    fb
                      .betweenUnboundedPreceding()
                      .andCurrentRow()
                      .excludeGroup(),
                  ),
              )
              .as('running_sum'),
          )

        testSql(query, dialect, {
          postgres: {
            sql: 'select sum("id") over(order by "first_name" range between unbounded preceding and current row exclude group) as "running_sum" from "person"',
            parameters: [],
          },
          mssql: {
            sql: 'select sum("id") over(order by "first_name" range between unbounded preceding and current row exclude group) as "running_sum" from "person"',
            parameters: [],
          },
        })
      })
    }
  })
}
