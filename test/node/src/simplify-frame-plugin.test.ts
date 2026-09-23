import {
  DummyDriver,
  Kysely,
  OverBuilderCallback,
  PostgresAdapter,
  PostgresIntrospector,
  PostgresQueryCompiler,
  SimplifyFramePlugin,
  sql,
} from '../../../'
import { Database, expect } from './test-setup.js'

describe('SimplifyFramePlugin', () => {
  let db: Kysely<Database>

  before(async () => {
    db = new Kysely({
      dialect: {
        createAdapter: () => new PostgresAdapter(),
        createDriver: () => new DummyDriver(),
        createIntrospector: (db) => new PostgresIntrospector(db),
        createQueryCompiler: () => new PostgresQueryCompiler(),
      },
      plugins: [new SimplifyFramePlugin()],
    })
  })

  function compileOver(over: OverBuilderCallback<Database, 'person'>) {
    return db
      .selectFrom('person')
      .select((eb) => eb.fn.sum('children').over(over).as('s'))
      .compile()
  }

  function expectOver(
    over: OverBuilderCallback<Database, 'person'>,
    expectedOver: string,
    expectedParameters: unknown[] = [],
  ) {
    const compiled = compileOver(over)

    expect(compiled.sql).to.equal(
      `select sum("children") ${expectedOver} as "s" from "person"`,
    )
    expect(compiled.parameters).to.eql(expectedParameters)
  }

  describe('with order by', () => {
    it('should remove `range between unbounded preceding and current row`', () => {
      expectOver(
        (ob) =>
          ob
            .orderBy('id')
            .range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
        'over(order by "id")',
      )
    })

    it('should remove the `range unbounded preceding` shorthand', () => {
      expectOver(
        (ob) => ob.orderBy('id').range((fb) => fb.unboundedPreceding()),
        'over(order by "id")',
      )
    })

    it('should keep `range between unbounded preceding and unbounded following`', () => {
      expectOver(
        (ob) =>
          ob
            .partitionBy('gender')
            .orderBy('id')
            .range((fb) =>
              fb.betweenUnboundedPreceding().andUnboundedFollowing(),
            ),
        'over(partition by "gender" order by "id" range between unbounded preceding and unbounded following)',
      )
    })
  })

  describe('without order by', () => {
    it('should remove `range between unbounded preceding and unbounded following`', () => {
      expectOver(
        (ob) =>
          ob.range((fb) =>
            fb.betweenUnboundedPreceding().andUnboundedFollowing(),
          ),
        'over()',
      )
    })

    it('should remove the frame and keep partition by', () => {
      expectOver(
        (ob) =>
          ob
            .partitionBy('gender')
            .range((fb) =>
              fb.betweenUnboundedPreceding().andUnboundedFollowing(),
            ),
        'over(partition by "gender")',
      )
    })

    it('should keep `range between unbounded preceding and current row`', () => {
      expectOver(
        (ob) =>
          ob.range((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
        'over(range between unbounded preceding and current row)',
      )
    })

    it('should keep the `range unbounded preceding` shorthand', () => {
      expectOver(
        (ob) => ob.range((fb) => fb.unboundedPreceding()),
        'over(range unbounded preceding)',
      )
    })
  })

  it('should keep `rows` frames', () => {
    expectOver(
      (ob) =>
        ob
          .orderBy('id')
          .rows((fb) => fb.betweenUnboundedPreceding().andCurrentRow()),
      'over(order by "id" rows between unbounded preceding and current row)',
    )
  })

  it('should keep `groups` frames', () => {
    expectOver(
      (ob) =>
        ob.groups((fb) =>
          fb.betweenUnboundedPreceding().andUnboundedFollowing(),
        ),
      'over(groups between unbounded preceding and unbounded following)',
    )
  })

  it('should keep frames with an exclusion', () => {
    expectOver(
      (ob) =>
        ob
          .orderBy('id')
          .range((fb) =>
            fb.betweenUnboundedPreceding().andCurrentRow().excludeNoOthers(),
          ),
      'over(order by "id" range between unbounded preceding and current row exclude no others)',
    )
  })

  it('should keep frames with offsets', () => {
    expectOver(
      (ob) =>
        ob.orderBy('id').range((fb) => fb.betweenPreceding(1).andCurrentRow()),
      'over(order by "id" range between $1 preceding and current row)',
      [1],
    )

    expectOver(
      (ob) =>
        ob
          .orderBy('id')
          .range((fb) =>
            fb.betweenUnboundedPreceding().andFollowing(sql.lit(0)),
          ),
      'over(order by "id" range between unbounded preceding and 0 following)',
    )
  })

  it('should keep frames with other bounds', () => {
    expectOver(
      (ob) =>
        ob.orderBy('id').range((fb) => fb.betweenCurrentRow().andCurrentRow()),
      'over(order by "id" range between current row and current row)',
    )
  })

  it('should simplify frames in subqueries', () => {
    const compiled = db
      .selectFrom((eb) =>
        eb
          .selectFrom('person')
          .select((eb) =>
            eb.fn
              .rowNumber()
              .over((ob) =>
                ob
                  .orderBy('id')
                  .range((fb) =>
                    fb.betweenUnboundedPreceding().andCurrentRow(),
                  ),
              )
              .as('rn'),
          )
          .as('p'),
      )
      .selectAll()
      .compile()

    expect(compiled.sql).to.equal(
      'select * from (select row_number() over(order by "id") as "rn" from "person") as "p"',
    )
  })
})
