import { describe, expect, test } from 'vitest';

import {
  normalizeStackNewlines,
  processStackFrames,
  processStackString,
} from './error-stack.js';
import { normalizeErrorStackOptions } from './error-options.js';
import { sanitizeMessage } from './error-sanitizer.js';
import { ErrorClassRegistry } from './error-class-registry.js';

const cwd = process.cwd();

const STACK = [
  'Error: boom',
  `    at userFn (${cwd}/app/user.ts:10:5)`,
  '    at Module._compile (node:internal/modules/cjs/loader:1256:14)',
  `    at walker (${cwd}/src/plainer.ts:200:3)`,
  `    at transformValue (${cwd}/src/transformer.ts:320:10)`,
  '    at processTicksAndRejections (node:internal/process/task_queues:95:5)',
  `    at main (${cwd}/app/main.ts:3:1)`,
].join('\n');

describe('normalizeErrorStackOptions', () => {
  test.each([null, undefined, 'string', 42, true])(
    'returns undefined for %s',
    input => {
      expect(normalizeErrorStackOptions(input)).toBeUndefined();
    }
  );

  test('applies defaults', () => {
    expect(normalizeErrorStackOptions({})).toEqual({
      mode: 'off',
      normalizeNewlines: false,
      trimLeadingWhitespace: true,
      maxStackLines: undefined,
      stripInternalFrames: 'none',
      redactPaths: 'none',
      includeCauses: 'none',
      maxCauseDepth: 16,
      sanitizeMessage: false,
      classFilter: [],
    });
  });

  test('keeps valid values', () => {
    expect(
      normalizeErrorStackOptions({
        mode: 'frames',
        normalizeNewlines: true,
        trimLeadingWhitespace: false,
        maxStackLines: 5,
        stripInternalFrames: 'node_and_superjson',
        redactPaths: 'strip_cwd',
        includeCauses: 'deep',
        maxCauseDepth: 3,
        sanitizeMessage: true,
        classFilter: ['TypeError'],
      })
    ).toEqual({
      mode: 'frames',
      normalizeNewlines: true,
      trimLeadingWhitespace: false,
      maxStackLines: 5,
      stripInternalFrames: 'node_and_superjson',
      redactPaths: 'strip_cwd',
      includeCauses: 'deep',
      maxCauseDepth: 3,
      sanitizeMessage: true,
      classFilter: ['TypeError'],
    });
  });

  test('invalid mode falls back to off', () => {
    expect(normalizeErrorStackOptions({ mode: 'verbose' })?.mode).toBe('off');
  });

  test.each([0, -1, 1.5, NaN, '3'])(
    'maxStackLines=%s behaves like mode=off',
    maxStackLines => {
      expect(
        normalizeErrorStackOptions({ mode: 'string', maxStackLines })?.mode
      ).toBe('off');
    }
  );

  test('unknown enum values fall back to none', () => {
    const options = normalizeErrorStackOptions({
      mode: 'string',
      stripInternalFrames: 'everything',
      redactPaths: 'hash',
      includeCauses: 'all',
    });
    expect(options?.stripInternalFrames).toBe('none');
    expect(options?.redactPaths).toBe('none');
    expect(options?.includeCauses).toBe('none');
  });

  test.each([1.5, '2', null])(
    'non-integer maxCauseDepth=%s falls back to includeCauses=none',
    maxCauseDepth => {
      expect(
        normalizeErrorStackOptions({ includeCauses: 'deep', maxCauseDepth })
          ?.includeCauses
      ).toBe('none');
    }
  );

  test('does not keep references to the input', () => {
    const classFilter = ['TypeError'];
    const input = { mode: 'string', classFilter };
    const options = normalizeErrorStackOptions(input)!;
    input.mode = 'frames';
    classFilter.push('RangeError');
    expect(options.mode).toBe('string');
    expect(options.classFilter).toEqual(['TypeError']);
  });
});

describe('normalizeStackNewlines', () => {
  test('converts CRLF and CR to LF', () => {
    expect(normalizeStackNewlines('a\r\nb\rc\nd')).toBe('a\nb\nc\nd');
  });
});

describe('processStackString', () => {
  test('trims leading whitespace on non-header lines by default', () => {
    expect(processStackString('  Error: x\n    at a\n\tat b')).toBe(
      '  Error: x\nat a\nat b'
    );
  });

  test('preserves leading whitespace when trimLeadingWhitespace=false', () => {
    expect(
      processStackString('Error: x\n    at a', { trimLeadingWhitespace: false })
    ).toBe('Error: x\n    at a');
  });

  test('normalizeNewlines defaults to false', () => {
    expect(processStackString('Error: x\r\n    at a\r\n    at b')).toBe(
      'Error: x\r\nat a\r\nat b'
    );
    expect(
      processStackString('Error: x\r\n    at a\r    at b', {
        normalizeNewlines: true,
      })
    ).toBe('Error: x\nat a\nat b');
  });

  test('maxStackLines counts the header line', () => {
    expect(processStackString(STACK, { maxStackLines: 1 })).toBe('Error: boom');
    expect(processStackString(STACK, { maxStackLines: 2 })).toBe(
      `Error: boom\nat userFn (${cwd}/app/user.ts:10:5)`
    );
  });

  test('strips node internal frames', () => {
    expect(
      processStackString(STACK, { stripInternalFrames: 'node' }).split('\n')
    ).toEqual([
      'Error: boom',
      `at userFn (${cwd}/app/user.ts:10:5)`,
      `at walker (${cwd}/src/plainer.ts:200:3)`,
      `at transformValue (${cwd}/src/transformer.ts:320:10)`,
      `at main (${cwd}/app/main.ts:3:1)`,
    ]);
  });

  test('strips superjson frames', () => {
    const stack = [
      'Error: x',
      '    at a (/p/src/index.ts:1:1)',
      '    at b (/p/src/plainer.ts:1:1)',
      '    at c (/p/src/transformer.ts:1:1)',
      '    at d (/p/src/other.ts:1:1)',
    ].join('\n');
    expect(
      processStackString(stack, { stripInternalFrames: 'superjson' })
    ).toBe('Error: x\nat d (/p/src/other.ts:1:1)');
  });

  test('strips node and superjson frames', () => {
    expect(
      processStackString(STACK, {
        stripInternalFrames: 'node_and_superjson',
      }).split('\n')
    ).toEqual([
      'Error: boom',
      `at userFn (${cwd}/app/user.ts:10:5)`,
      `at main (${cwd}/app/main.ts:3:1)`,
    ]);
  });

  test('never strips the header line', () => {
    expect(
      processStackString('Error: node:internal src/index.ts\n    at x', {
        stripInternalFrames: 'node_and_superjson',
      })
    ).toBe('Error: node:internal src/index.ts\nat x');
  });

  test('unknown stripInternalFrames / redactPaths fall back to none', () => {
    expect(
      processStackString(STACK, {
        stripInternalFrames: 'bogus' as any,
        redactPaths: 'bogus' as any,
      })
    ).toBe(processStackString(STACK));
  });

  test('redactPaths=basename keeps only file names', () => {
    const stack = [
      'Error: x',
      '    at a (/home/me/project/src/a.ts:1:2)',
      '    at b (file:///home/me/project/b.mjs:3:4)',
      '    at C:\\Users\\me\\c.js:5:6',
      '    at d (/app/node_modules/@scope/pkg/index.js:7:8)',
      '    at new Promise (<anonymous>)',
    ].join('\n');
    expect(processStackString(stack, { redactPaths: 'basename' })).toBe(
      [
        'Error: x',
        'at a (a.ts:1:2)',
        'at b (b.mjs:3:4)',
        'at c.js:5:6',
        'at d (index.js:7:8)',
        'at new Promise (<anonymous>)',
      ].join('\n')
    );
  });

  test('redactPaths=strip_cwd removes the cwd prefix', () => {
    const stack = [
      'Error: x',
      `    at a (${cwd}/src/a.ts:1:2)`,
      `    at file://${cwd}/b.mjs:3:4`,
      '    at c (/elsewhere/c.ts:5:6)',
    ].join('\n');
    expect(processStackString(stack, { redactPaths: 'strip_cwd' })).toBe(
      [
        'Error: x',
        'at a (src/a.ts:1:2)',
        'at b.mjs:3:4',
        'at c (/elsewhere/c.ts:5:6)',
      ].join('\n')
    );
  });

  test('redacts before stripping, so basename-redacted superjson frames survive', () => {
    expect(
      processStackString(STACK, {
        redactPaths: 'basename',
        stripInternalFrames: 'superjson',
      }).split('\n')
    ).toEqual([
      'Error: boom',
      'at userFn (user.ts:10:5)',
      'at Module._compile (loader:1256:14)',
      'at walker (plainer.ts:200:3)',
      'at transformValue (transformer.ts:320:10)',
      'at processTicksAndRejections (task_queues:95:5)',
      'at main (main.ts:3:1)',
    ]);
  });

  test('limits before stripping', () => {
    expect(
      processStackString(STACK, {
        maxStackLines: 3,
        stripInternalFrames: 'node',
      }).split('\n')
    ).toEqual(['Error: boom', `at userFn (${cwd}/app/user.ts:10:5)`]);
  });
});

describe('processStackFrames', () => {
  test('uses the header as the first frame', () => {
    expect(processStackFrames('Error: x\n    at a\n    at b')).toEqual([
      { raw: 'Error: x' },
      { raw: 'at a' },
      { raw: 'at b' },
    ]);
  });

  test('strips before redacting', () => {
    expect(
      processStackFrames(STACK, {
        redactPaths: 'basename',
        stripInternalFrames: 'superjson',
      })
    ).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at userFn (user.ts:10:5)' },
      { raw: 'at Module._compile (loader:1256:14)' },
      { raw: 'at processTicksAndRejections (task_queues:95:5)' },
      { raw: 'at main (main.ts:3:1)' },
    ]);
  });

  test('strips before limiting', () => {
    expect(
      processStackFrames(STACK, {
        maxStackLines: 3,
        stripInternalFrames: 'node',
        redactPaths: 'strip_cwd',
      })
    ).toEqual([
      { raw: 'Error: boom' },
      { raw: 'at userFn (app/user.ts:10:5)' },
      { raw: 'at walker (src/plainer.ts:200:3)' },
    ]);
  });
});

describe('sanitizeMessage', () => {
  test('redacts URLs, emails and IPv4 addresses', () => {
    expect(
      sanitizeMessage(
        'GET https://api.example.com/v1/users?id=1 failed for jane.doe+ops@example.co.uk from 192.168.0.12:8080'
      )
    ).toBe('GET [redacted] failed for [redacted] from [redacted]:8080');
  });

  test('leaves trailing punctuation and plain text alone', () => {
    expect(sanitizeMessage('See http://example.com/docs.')).toBe(
      'See [redacted].'
    );
    expect(sanitizeMessage('version 1.2.3 is fine')).toBe(
      'version 1.2.3 is fine'
    );
    expect(sanitizeMessage('999.1.1.1 is not an IP')).toBe(
      '999.1.1.1 is not an IP'
    );
  });
});

describe('ErrorClassRegistry', () => {
  test('registers and looks up processors by name', () => {
    const registry = new ErrorClassRegistry();
    const fn = (v: any) => v;
    expect(registry.has('TypeError')).toBe(false);
    expect(registry.getProcessor('TypeError')).toBeUndefined();
    registry.register('TypeError', fn);
    expect(registry.has('TypeError')).toBe(true);
    expect(registry.getProcessor('TypeError')).toBe(fn);
  });
});
