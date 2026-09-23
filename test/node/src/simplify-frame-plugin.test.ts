import { type FrameBuilderCallback, SimplifyFramePlugin, sql } from '../../..'

import {
  clearDatabase,
  destroyTest,
  initTest,
  TestContext,
  testSql,
  expect,
  insertDefaultDataSet,
  DIALECTS,
} from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: simplify frame plugin`, () => {
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

    function quote(sql: string): string {
      return dialect === 'mysql' ? sql.replace(/"/g, '`') : sql
    }

    function testFrame(
      orderBy: boolean,
      mode: 'rows' | 'range' | 'groups',
      frame: FrameBuilderCallback,
      expectedOver: string,
    ) {
      const query = ctx.db
        .withPlugin(new SimplifyFramePlugin())
        .selectFrom('person')
        .select((eb) =>
          eb.fn
            .sum<number>('id')
            .over((ob) => (orderBy ? ob.orderBy('id') : ob)[mode](frame))
            .as('s'),
        )

      const expected = {
        sql: quote(`select sum("id") ${expectedOver} as "s" from "person"`),
        parameters: [],
      }

      testSql(query, dialect, {
        postgres: expected,
        mysql: expected,
        mssql: expected,
        sqlite: expected,
      })
    }

    it('should strip range between unbounded preceding and current row when ordered', () => {
      testFrame(
        true,
        'range',
        (fb) => fb.betweenUnboundedPreceding().andCurrentRow(),
        'over(order by "id")',
      )
    })

    it('should strip range unbounded preceding shorthand when ordered', () => {
      testFrame(
        true,
        'range',
        (fb) => fb.unboundedPreceding(),
        'over(order by "id")',
      )
    })

    it('should strip range between unbounded preceding and unbounded following when unordered', () => {
      testFrame(
        false,
        'range',
        (fb) => fb.betweenUnboundedPreceding().andUnboundedFollowing(),
        'over()',
      )
    })

    it('should keep the unordered default when ordered', () => {
      testFrame(
        true,
        'range',
        (fb) => fb.betweenUnboundedPreceding().andUnboundedFollowing(),
        'over(order by "id" range between unbounded preceding and unbounded following)',
      )
    })

    it('should keep the ordered default when unordered', () => {
      testFrame(
        false,
        'range',
        (fb) => fb.betweenUnboundedPreceding().andCurrentRow(),
        'over(range between unbounded preceding and current row)',
      )
    })

    it('should keep rows and groups modes', () => {
      testFrame(
        true,
        'rows',
        (fb) => fb.betweenUnboundedPreceding().andCurrentRow(),
        'over(order by "id" rows between unbounded preceding and current row)',
      )
      testFrame(
        true,
        'groups',
        (fb) => fb.betweenUnboundedPreceding().andCurrentRow(),
        'over(order by "id" groups between unbounded preceding and current row)',
      )
      testFrame(
        false,
        'rows',
        (fb) => fb.betweenUnboundedPreceding().andUnboundedFollowing(),
        'over(rows between unbounded preceding and unbounded following)',
      )
    })

    it('should keep frames with an exclusion clause', () => {
      testFrame(
        true,
        'range',
        (fb) =>
          fb.betweenUnboundedPreceding().andCurrentRow().excludeNoOthers(),
        'over(order by "id" range between unbounded preceding and current row exclude no others)',
      )
    })

    it('should keep frames with non-default bounds or offsets', () => {
      testFrame(
        true,
        'range',
        (fb) => fb.betweenPreceding(sql.lit(1)).andCurrentRow(),
        'over(order by "id" range between 1 preceding and current row)',
      )
      testFrame(
        true,
        'range',
        (fb) => fb.betweenUnboundedPreceding().andFollowing(sql.lit(1)),
        'over(order by "id" range between unbounded preceding and 1 following)',
      )
      testFrame(
        true,
        'range',
        (fb) => fb.currentRow(),
        'over(order by "id" range current row)',
      )
    })

    it('should keep partition by when stripping', async () => {
      const query = ctx.db
        .withPlugin(new SimplifyFramePlugin())
        .selectFrom('person')
        .select((eb) =>
          eb.fn
            .count<number>('id')
            .over((ob) =>
              ob
                .partitionBy('gender')
                .range((fb) =>
                  fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                ),
            )
            .as('c'),
        )
        .orderBy('id')

      const expected = {
        sql: quote(
          'select count("id") over(partition by "gender") as "c" from "person" order by "id"',
        ),
        parameters: [],
      }

      testSql(query, dialect, {
        postgres: expected,
        mysql: expected,
        mssql: expected,
        sqlite: expected,
      })

      const result = await query.execute()
      expect(result.map((it) => Number(it.c))).to.eql([1, 2, 2])
    })
  })
}
