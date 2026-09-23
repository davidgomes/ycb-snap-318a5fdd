import { sql, SimplifyFramePlugin } from '../../../'
import {
  DummyDriver,
  Kysely,
  MssqlAdapter,
  MssqlIntrospector,
  MssqlQueryCompiler,
  MysqlAdapter,
  MysqlIntrospector,
  MysqlQueryCompiler,
  PostgresAdapter,
  PostgresIntrospector,
  PostgresQueryCompiler,
  SqliteAdapter,
  SqliteIntrospector,
  SqliteQueryCompiler,
} from '../../../'
import { BuiltInDialect, Database, DIALECTS, expect } from './test-setup.js'

for (const dialect of DIALECTS) {
  describe(`${dialect}: grouping and window frames`, () => {
    const db = createDb(dialect)

    it('composes group by with cube, rollup and grouping sets', () => {
      const query = db
        .selectFrom('person')
        .select((eb) => [
          'gender',
          eb.fn.countAll<number>().as('count'),
          eb.fn.grouping<number>('gender').as('gender_grouping'),
        ])
        .groupBy('first_name')
        .groupByCube('gender', 'last_name')
        .groupByRollup(sql`lower(first_name)`)
        .groupByGroupingSets(['gender'], ['first_name', 'last_name'])

      expect(query.compile().sql).to.equal(
        `select ${id(dialect, 'gender')}, count(*) as ${id(dialect, 'count')}, grouping(${id(dialect, 'gender')}) as ${id(dialect, 'gender_grouping')} from ${id(dialect, 'person')} group by ${id(dialect, 'first_name')}, cube(${id(dialect, 'gender')}, ${id(dialect, 'last_name')}), rollup(lower(first_name)), grouping sets((${id(dialect, 'gender')}), (${id(dialect, 'first_name')}, ${id(dialect, 'last_name')}))`,
      )
      expect(query.compile().parameters).to.eql([])
    })

    it('compiles window functions, null treatment and frame offsets', () => {
      const query = db.selectFrom('person').select((eb) => [
        eb.fn.rowNumber<number>().over().as('rn'),
        eb.fn
          .rank<number>()
          .over((ob) => ob.orderBy('id'))
          .as('rank'),
        eb.fn
          .denseRank<number>()
          .over((ob) => ob.partitionBy('gender'))
          .as('dense'),
        eb.fn.percentRank<number>().over().as('pct'),
        eb.fn.cumeDist<number>().over().as('cume'),
        eb.fn
          .ntile<number>(4)
          .over((ob) => ob.orderBy('id'))
          .as('tile'),
        eb.fn
          .firstValue<string>('first_name')
          .respectNulls()
          .filterWhere('gender', '=', 'female')
          .over((ob) => ob.orderBy('id'))
          .as('first'),
        eb.fn
          .lastValue<string | null>('last_name')
          .over((ob) => ob.orderBy('id').rows((fb) => fb.unboundedPreceding()))
          .as('last'),
        eb.fn
          .nthValue<string>('first_name', 2n)
          .over((ob) => ob.orderBy('id'))
          .as('nth'),
        eb.fn
          .lag<string | null>('first_name', 2, 0)
          .ignoreNulls()
          .over((ob) =>
            ob
              .partitionBy('gender')
              .orderBy('id')
              .rows((fb) =>
                fb
                  .betweenPreceding(1)
                  .andFollowing(sql`1`)
                  .excludeCurrentRow(),
              ),
          )
          .as('prev'),
        eb.fn
          .lead<string>('first_name')
          .over((ob) =>
            ob.groups((fb) =>
              fb.betweenCurrentRow().andUnboundedFollowing().excludeGroup(),
            ),
          )
          .as('next'),
      ])

      const compiled = query.compile()

      expect(compiled.sql).to.equal(
        [
          'select',
          `row_number() over() as ${id(dialect, 'rn')},`,
          `rank() over(order by ${id(dialect, 'id')}) as ${id(dialect, 'rank')},`,
          `dense_rank() over(partition by ${id(dialect, 'gender')}) as ${id(dialect, 'dense')},`,
          `percent_rank() over() as ${id(dialect, 'pct')},`,
          `cume_dist() over() as ${id(dialect, 'cume')},`,
          `ntile(${ph(dialect, 1)}) over(order by ${id(dialect, 'id')}) as ${id(dialect, 'tile')},`,
          `first_value(${id(dialect, 'first_name')}) respect nulls filter(where ${id(dialect, 'gender')} = ${ph(dialect, 2)}) over(order by ${id(dialect, 'id')}) as ${id(dialect, 'first')},`,
          `last_value(${id(dialect, 'last_name')}) over(order by ${id(dialect, 'id')} rows unbounded preceding) as ${id(dialect, 'last')},`,
          `nth_value(${id(dialect, 'first_name')}, ${ph(dialect, 3)}) over(order by ${id(dialect, 'id')}) as ${id(dialect, 'nth')},`,
          `lag(${id(dialect, 'first_name')}, ${ph(dialect, 4)}, ${ph(dialect, 5)}) ignore nulls over(partition by ${id(dialect, 'gender')} order by ${id(dialect, 'id')} rows between ${ph(dialect, 6)} preceding and 1 following exclude current row) as ${id(dialect, 'prev')},`,
          `lead(${id(dialect, 'first_name')}) over(groups between current row and unbounded following exclude group) as ${id(dialect, 'next')}`,
          `from ${id(dialect, 'person')}`,
        ].join(' '),
      )
      expect(compiled.parameters).to.eql([4, 'female', 2n, 2, 0, 1])
    })

    describe('SimplifyFramePlugin', () => {
      const plugin = new SimplifyFramePlugin()

      it('strips the ordered range default, including the unbounded preceding shorthand', () => {
        const between = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob
                  .orderBy('id')
                  .range((fb) =>
                    fb.betweenUnboundedPreceding().andCurrentRow(),
                  ),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const shorthand = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob.orderBy('id').range((fb) => fb.unboundedPreceding()),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const expected = `select sum(${id(dialect, 'children')}) over(order by ${id(dialect, 'id')}) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`

        expect(between.compile().sql).to.equal(expected)
        expect(shorthand.compile().sql).to.equal(expected)
      })

      it('strips the unordered range default and keeps a different extent', () => {
        const redundant = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob
                  .partitionBy('gender')
                  .range((fb) =>
                    fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                  ),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const kept = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob.range((fb) =>
                  fb.betweenUnboundedPreceding().andCurrentRow(),
                ),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        expect(redundant.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(partition by ${id(dialect, 'gender')}) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(kept.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(range between unbounded preceding and current row) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
      })

      it('preserves rows, groups, exclusions, non-default bounds and expression offsets', () => {
        const rows = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob
                  .orderBy('id')
                  .rows((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const groups = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob.groups((fb) =>
                  fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                ),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const exclusion = db
          .selectFrom('person')
          .select((eb) =>
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
              .as('total'),
          )
          .withPlugin(plugin)

        const following = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob
                  .orderBy('id')
                  .range((fb) =>
                    fb.betweenUnboundedPreceding().andUnboundedFollowing(),
                  ),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const expressionOffset = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob.orderBy('id').range((fb) => fb.preceding(sql`1`)),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const numericOffset = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) =>
                ob.orderBy('id').rows((fb) => fb.preceding(3).excludeTies()),
              )
              .as('total'),
          )
          .withPlugin(plugin)

        const unorderedShorthand = db
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .sum<number>('children')
              .over((ob) => ob.range((fb) => fb.unboundedPreceding()))
              .as('total'),
          )
          .withPlugin(plugin)

        expect(rows.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(order by ${id(dialect, 'id')} rows between unbounded preceding and current row) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(groups.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(groups between unbounded preceding and unbounded following) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(exclusion.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(order by ${id(dialect, 'id')} range between unbounded preceding and current row exclude no others) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(following.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(order by ${id(dialect, 'id')} range between unbounded preceding and unbounded following) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(expressionOffset.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(order by ${id(dialect, 'id')} range 1 preceding) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(expressionOffset.compile().parameters).to.eql([])
        expect(numericOffset.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(order by ${id(dialect, 'id')} rows ${ph(dialect, 1)} preceding exclude ties) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
        expect(numericOffset.compile().parameters).to.eql([3])
        expect(unorderedShorthand.compile().sql).to.equal(
          `select sum(${id(dialect, 'children')}) over(range unbounded preceding) as ${id(dialect, 'total')} from ${id(dialect, 'person')}`,
        )
      })
    })
  })
}

function createDb(dialect: BuiltInDialect): Kysely<Database> {
  switch (dialect) {
    case 'postgres':
      return new Kysely({
        dialect: {
          createAdapter: () => new PostgresAdapter(),
          createDriver: () => new DummyDriver(),
          createIntrospector: (db) => new PostgresIntrospector(db),
          createQueryCompiler: () => new PostgresQueryCompiler(),
        },
      })
    case 'mysql':
      return new Kysely({
        dialect: {
          createAdapter: () => new MysqlAdapter(),
          createDriver: () => new DummyDriver(),
          createIntrospector: (db) => new MysqlIntrospector(db),
          createQueryCompiler: () => new MysqlQueryCompiler(),
        },
      })
    case 'mssql':
      return new Kysely({
        dialect: {
          createAdapter: () => new MssqlAdapter(),
          createDriver: () => new DummyDriver(),
          createIntrospector: (db) => new MssqlIntrospector(db),
          createQueryCompiler: () => new MssqlQueryCompiler(),
        },
      })
    case 'sqlite':
      return new Kysely({
        dialect: {
          createAdapter: () => new SqliteAdapter(),
          createDriver: () => new DummyDriver(),
          createIntrospector: (db) => new SqliteIntrospector(db),
          createQueryCompiler: () => new SqliteQueryCompiler(),
        },
      })
  }
}

function id(dialect: BuiltInDialect, name: string): string {
  return dialect === 'mysql' ? `\`${name}\`` : `"${name}"`
}

function ph(dialect: BuiltInDialect, index: number): string {
  if (dialect === 'postgres') {
    return `$${index}`
  }

  if (dialect === 'mssql') {
    return `@${index}`
  }

  return '?'
}
