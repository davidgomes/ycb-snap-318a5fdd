import { Expect } from 'type-tests/utils.ts';
import { integer, pgTable } from '~/pg-core/index.ts';
import { firstValue, lag, lead, nthValue } from '~/sql/functions/window.ts';
import type { Equal } from '~/utils.ts';

const users = pgTable('users', {
	id: integer('id').notNull(),
	score: integer('score'),
});

const lagged = lag(users.score).over();
const laggedWithDefault = lag(users.score, 1, users.id).over();
const led = lead(users.id).over();
const ledWithDefault = lead(users.id, 1, users.id).over();
const first = firstValue(users.score).over();
const nth = nthValue(users.score, 2).over();

Expect<Equal<number | null, typeof lagged._.type>>;
Expect<Equal<number, typeof laggedWithDefault._.type>>;
Expect<Equal<number | null, typeof led._.type>>;
Expect<Equal<number, typeof ledWithDefault._.type>>;
Expect<Equal<number | null, typeof first._.type>>;
Expect<Equal<number | null, typeof nth._.type>>;
