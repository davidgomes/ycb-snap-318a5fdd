import { SimplifyFramePlugin, sql } from '../../../'
import { createDummyDb, sqlExpectation } from './dummy-db.js'
import { DIALECTS, testSql } from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: window functions and frames`, () => {
    const db = createDummyDb(dialect)

    it('should compile ranking accessors', () => {
      const query = db.selectFrom('person').select((eb) => [
        eb.fn
          .rowNumber<number>()
          .over((ob) => ob.orderBy('id'))
          .as('row_number'),
        eb.fn
          .rank<number>()
          .over((ob) => ob.partitionBy('gender'))
          .as('rank'),
        eb.fn.denseRank<bigint>().over().as('dense_rank'),
        eb.fn.percentRank<number>().over().as('percent_rank'),
        eb.fn.cumeDist<string>().over().as('cume_dist'),
        eb.fn
          .ntile<number>(4)
          .over((ob) => ob.orderBy('id'))
          .as('bucket'),
      ])

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select row_number() over(order by "id") as "row_number", rank() over(partition by "gender") as "rank", dense_rank() over() as "dense_rank", percent_rank() over() as "percent_rank", cume_dist() over() as "cume_dist", ntile($1) over(order by "id") as "bucket" from "person"',
          [4],
        ),
      )
    })

    it('should compile value accessors and place null treatment before later clauses', () => {
      const query = db.selectFrom('person').select((eb) => [
        eb.fn
          .firstValue<string | null>('first_name')
          .ignoreNulls()
          .filterWhere('gender', '=', 'female')
          .over((ob) => ob.orderBy('id'))
          .as('first_name'),
        eb.fn
          .lastValue<string>('last_name')
          .respectNulls()
          .over((ob) => ob.partitionBy('gender').orderBy('id', 'desc'))
          .as('last_name'),
        eb.fn.nthValue<string | null>('first_name', 2n).as('nth_name'),
        eb.fn
          .lag<number | null>('children', 1, 0)
          .respectNulls()
          .over((ob) => ob.orderBy('id'))
          .as('previous_children'),
        eb.fn.lead<number>('children', 1).ignoreNulls().as('next_children'),
      ])

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select first_value("first_name") ignore nulls filter(where "gender" = $1) over(order by "id") as "first_name", last_value("last_name") respect nulls over(partition by "gender" order by "id" desc) as "last_name", nth_value("first_name", $2) as "nth_name", lag("children", $3, $4) respect nulls over(order by "id") as "previous_children", lead("children", $5) ignore nulls as "next_children" from "person"',
          ['female', 2n, 1, 0, 1],
        ),
      )
    })

    it('should compile rows, range and groups extents with offsets and exclusion', () => {
      const query = db.selectFrom('person').select((eb) => [
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .partitionBy('gender')
              .orderBy('id')
              .rows((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('rows_frame'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.orderBy('id').range((fb) =>
              fb
                .betweenPreceding(1)
                .andFollowing(sql`2`)
                .excludeTies(),
            ),
          )
          .as('range_frame'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.groups((fb) =>
              fb.betweenCurrentRow().andUnboundedFollowing().excludeGroup(),
            ),
          )
          .as('groups_frame'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.rows((fb) => fb.unboundedPreceding().excludeCurrentRow()),
          )
          .as('rows_short'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.orderBy('id').range((fb) => fb.currentRow().excludeNoOthers()),
          )
          .as('range_current'),
        eb.fn
          .sum<number>('children')
          .over((ob) => ob.rows((fb) => fb.following(3n)))
          .as('rows_following'),
        eb.fn
          .sum<number>('children')
          .over((ob) => ob.rows((fb) => fb.unboundedFollowing()))
          .as('rows_unbounded_following'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.orderBy('id').range((fb) =>
              fb
                .betweenFollowing(sql`4`)
                .andPreceding(5)
                .excludeCurrentRow(),
            ),
          )
          .as('range_following_to_preceding'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.groups((fb) => fb.betweenCurrentRow().andUnboundedPreceding()),
          )
          .as('groups_and_unbounded_preceding'),
      ])

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select sum("children") over(partition by "gender" order by "id" rows between unbounded preceding and current row) as "rows_frame", sum("children") over(order by "id" range between $1 preceding and 2 following exclude ties) as "range_frame", sum("children") over(groups between current row and unbounded following exclude group) as "groups_frame", sum("children") over(rows unbounded preceding exclude current row) as "rows_short", sum("children") over(order by "id" range current row exclude no others) as "range_current", sum("children") over(rows $2 following) as "rows_following", sum("children") over(rows unbounded following) as "rows_unbounded_following", sum("children") over(order by "id" range between 4 following and $3 preceding exclude current row) as "range_following_to_preceding", sum("children") over(groups between current row and unbounded preceding) as "groups_and_unbounded_preceding" from "person"',
          [1, 3n, 5],
        ),
      )
    })
  })

  describe(`${dialect}: SimplifyFramePlugin`, () => {
    const db = createDummyDb(dialect, [new SimplifyFramePlugin()])

    it('should drop the ordered and unordered range defaults, including the shorthand', () => {
      const query = db.selectFrom('person').select((eb) => [
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('ordered_between'),
        eb.fn
          .sum<number>('children')
          .over((ob) => ob.orderBy('id').range((fb) => fb.unboundedPreceding()))
          .as('ordered_short'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .partitionBy('gender')
              .range((fb) =>
                fb.betweenUnboundedPreceding().andUnboundedFollowing(),
              ),
          )
          .as('unordered_between'),
      ])

      testSql(
        query,
        dialect,
        sqlExpectation(
          'select sum("children") over(order by "id") as "ordered_between", sum("children") over(order by "id") as "ordered_short", sum("children") over(partition by "gender") as "unordered_between" from "person"',
          [],
        ),
      )
    })

    it('should keep rows, groups, exclusion, non-default bounds and expression offsets', () => {
      const query = db.selectFrom('person').select((eb) => [
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .orderBy('id')
              .rows((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('rows_default_shape'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.groups((fb) =>
              fb.betweenUnboundedPreceding().andUnboundedFollowing(),
            ),
          )
          .as('groups_default_shape'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) =>
                fb
                  .betweenUnboundedPreceding()
                  .andCurrentRow()
                  .excludeNoOthers(),
              ),
          )
          .as('with_exclusion'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob.range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
          )
          .as('unordered_current_row'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) =>
                fb.betweenUnboundedPreceding().andUnboundedFollowing(),
              ),
          )
          .as('ordered_unbounded_following'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenPreceding(1).andCurrentRow()),
          )
          .as('numeric_offset'),
        eb.fn
          .sum<number>('children')
          .over((ob) =>
            ob
              .orderBy('id')
              .range((fb) => fb.betweenPreceding(sql`1`).andCurrentRow()),
          )
          .as('expression_offset'),
        eb.fn
          .sum<number>('children')
          .over((ob) => ob.range((fb) => fb.unboundedPreceding()))
          .as('unordered_shorthand'),
      ])

      testSql(
        query,
        dialect,
        sqlExpectation(
          [
            'select',
            'sum("children") over(order by "id" rows between unbounded preceding and current row) as "rows_default_shape",',
            'sum("children") over(groups between unbounded preceding and unbounded following) as "groups_default_shape",',
            'sum("children") over(order by "id" range between unbounded preceding and current row exclude no others) as "with_exclusion",',
            'sum("children") over(range between unbounded preceding and current row) as "unordered_current_row",',
            'sum("children") over(order by "id" range between unbounded preceding and unbounded following) as "ordered_unbounded_following",',
            'sum("children") over(order by "id" range between $1 preceding and current row) as "numeric_offset",',
            'sum("children") over(order by "id" range between 1 preceding and current row) as "expression_offset",',
            'sum("children") over(range unbounded preceding) as "unordered_shorthand"',
            'from "person"',
          ].join(' '),
          [1],
        ),
      )
    })
  })
}
