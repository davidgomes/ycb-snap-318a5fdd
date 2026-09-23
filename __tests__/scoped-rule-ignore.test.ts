import dedent from 'ts-dedent';
import RemoveMultipleSpaces from '../src/rules/remove-multiple-spaces';
import TrailingSpaces from '../src/rules/trailing-spaces';
import {RulesRunner} from '../src/rules-runner';
import {IgnoreTypes, ignoreListOfTypes} from '../src/utils/ignore-types';
import {getAllCustomIgnoreSectionsInText} from '../src/utils/scoped-rule-ignore';

const trailingSpaces = TrailingSpaces.getRule();
const removeMultipleSpaces = RemoveMultipleSpaces.getRule();

function lintTrailing(text: string): string {
  return trailingSpaces.apply(text);
}

function lintMultiple(text: string): string {
  return removeMultipleSpaces.apply(text);
}

describe('Scoped per-rule ignore markers', () => {
  it('disables every rule until enable, and leaves an unclosed scope in effect through EOF', () => {
    const closed = [
      'keep me   ',
      '<!-- linter-disable -->',
      'stay   ',
      '<!-- linter-enable -->',
      'trim me   ',
    ].join('\n');
    expect(lintTrailing(closed)).toBe([
      'keep me',
      '<!-- linter-disable -->',
      'stay   ',
      '<!-- linter-enable -->',
      'trim me',
    ].join('\n'));

    const unclosed = [
      '<!-- linter-disable -->',
      'stay   ',
      'also stay   ',
    ].join('\n');
    expect(lintTrailing(unclosed)).toBe(unclosed);
  });

  it('accepts Obsidian comments and a rule list', () => {
    const text = [
      '%% linter-disable trailing-spaces %%',
      'stay   ',
      'a   b',
      '%% linter-enable %%',
      'trim   ',
      'c   d',
    ].join('\n');

    expect(lintTrailing(text)).toBe([
      '%% linter-disable trailing-spaces %%',
      'stay   ',
      'a   b',
      '%% linter-enable %%',
      'trim',
      'c   d',
    ].join('\n'));
    expect(lintMultiple(text)).toBe([
      '%% linter-disable trailing-spaces %%',
      'stay   ',
      'a b',
      '%% linter-enable %%',
      'trim   ',
      'c d',
    ].join('\n'));
  });

  it('normalizes rule lists without case sensitivity and ignores blanks, duplicates, and unknown aliases', () => {
    const disabled = [
      '<!-- linter-disable Trailing-Spaces, trailing-spaces, , nope -->',
      'stay   ',
    ].join('\n');
    expect(lintTrailing(disabled)).toBe(disabled);

    const noEffect = [
      '<!-- linter-disable nope, -->',
      'trim   ',
      '<!-- linter-disable , , -->',
      'also trim   ',
    ].join('\n');
    expect(lintTrailing(noEffect)).toBe([
      '<!-- linter-disable nope, -->',
      'trim',
      '<!-- linter-disable , , -->',
      'also trim',
    ].join('\n'));
  });

  it('pops the nearest scope when enable has no list, and does nothing when that list normalizes empty', () => {
    const text = [
      '<!-- linter-disable trailing-spaces -->',
      'stay   ',
      '<!-- linter-enable nope -->',
      'still   ',
      '<!-- linter-enable -->',
      'trim   ',
    ].join('\n');
    expect(lintTrailing(text)).toBe([
      '<!-- linter-disable trailing-spaces -->',
      'stay   ',
      '<!-- linter-enable nope -->',
      'still   ',
      '<!-- linter-enable -->',
      'trim',
    ].join('\n'));
  });

  it('removes listed rules from the nearest scope and closes a specific scope once it is empty', () => {
    const text = [
      '<!-- linter-disable trailing-spaces -->',
      '<!-- linter-disable remove-multiple-spaces -->',
      'a   b   ',
      '<!-- linter-enable remove-multiple-spaces -->',
      'c   d   ',
      '<!-- linter-enable -->',
      'e   f   ',
    ].join('\n');

    expect(lintTrailing(text)).toBe([
      '<!-- linter-disable trailing-spaces -->',
      '<!-- linter-disable remove-multiple-spaces -->',
      'a   b   ',
      '<!-- linter-enable remove-multiple-spaces -->',
      'c   d   ',
      '<!-- linter-enable -->',
      'e   f',
    ].join('\n'));
    expect(lintMultiple(text)).toBe([
      '<!-- linter-disable trailing-spaces -->',
      '<!-- linter-disable remove-multiple-spaces -->',
      'a   b   ',
      '<!-- linter-enable remove-multiple-spaces -->',
      'c d   ',
      '<!-- linter-enable -->',
      'e f   ',
    ].join('\n'));
  });

  it('can re-enable one rule inside a disable-all scope without closing that scope', () => {
    const text = [
      '<!-- linter-disable -->',
      'a   b   ',
      '<!-- linter-enable trailing-spaces -->',
      'c   d   ',
      '<!-- linter-enable -->',
      'e   f   ',
    ].join('\n');

    expect(lintTrailing(text)).toBe([
      '<!-- linter-disable -->',
      'a   b   ',
      '<!-- linter-enable trailing-spaces -->',
      'c   d',
      '<!-- linter-enable -->',
      'e   f',
    ].join('\n'));
    expect(lintMultiple(text)).toBe([
      '<!-- linter-disable -->',
      'a   b   ',
      '<!-- linter-enable trailing-spaces -->',
      'c   d   ',
      '<!-- linter-enable -->',
      'e f   ',
    ].join('\n'));
  });

  it('re-enables a rule on the outer scope only after the nearer specific scope no longer disables it', () => {
    const text = [
      '<!-- linter-disable -->',
      '<!-- linter-disable trailing-spaces -->',
      '<!-- linter-enable trailing-spaces -->',
      'still blocked   ',
      '<!-- linter-enable trailing-spaces -->',
      'now trimmed   ',
    ].join('\n');

    expect(lintTrailing(text)).toBe([
      '<!-- linter-disable -->',
      '<!-- linter-disable trailing-spaces -->',
      '<!-- linter-enable trailing-spaces -->',
      'still blocked   ',
      '<!-- linter-enable trailing-spaces -->',
      'now trimmed',
    ].join('\n'));
  });

  it('applies line-scoped disables, clamps them to EOF, and ignores an invalid count', () => {
    expect(lintTrailing([
      '<!-- linter-disable-next-line trailing-spaces -->',
      'stay   ',
      'trim   ',
    ].join('\n'))).toBe([
      '<!-- linter-disable-next-line trailing-spaces -->',
      'stay   ',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '%% linter-disable-next-n-lines: 2 trailing-spaces %%',
      'a   ',
      'b   ',
      'c   ',
    ].join('\n'))).toBe([
      '%% linter-disable-next-n-lines: 2 trailing-spaces %%',
      'a   ',
      'b   ',
      'c',
    ].join('\n'));

    expect(lintTrailing([
      '<!-- linter-disable-next-n-lines: 2 trailing-spaces -->',
      '',
      'a   ',
      'b   ',
    ].join('\n'))).toBe([
      '<!-- linter-disable-next-n-lines: 2 trailing-spaces -->',
      '',
      'a   ',
      'b',
    ].join('\n'));

    expect(lintTrailing([
      '<!-- linter-disable-next-n-lines: 9 -->',
      'a   ',
      'b   ',
    ].join('\n'))).toBe([
      '<!-- linter-disable-next-n-lines: 9 -->',
      'a   ',
      'b   ',
    ].join('\n'));

    expect(lintTrailing([
      'before   ',
      '<!-- linter-disable-next-line -->',
    ].join('\n'))).toBe([
      'before',
      '<!-- linter-disable-next-line -->',
    ].join('\n'));

    expect(lintTrailing([
      '<!-- linter-disable-next-n-lines: 0 trailing-spaces -->',
      'a   ',
      '<!-- linter-disable-next-n-lines: 1.5 trailing-spaces -->',
      'b   ',
      '<!-- linter-disable-next-n-lines: +3 trailing-spaces -->',
      'c   ',
      '<!-- linter-disable-next-n-lines: 01 trailing-spaces -->',
      'd   ',
      'e   ',
    ].join('\n'))).toBe([
      '<!-- linter-disable-next-n-lines: 0 trailing-spaces -->',
      'a',
      '<!-- linter-disable-next-n-lines: 1.5 trailing-spaces -->',
      'b',
      '<!-- linter-disable-next-n-lines: +3 trailing-spaces -->',
      'c',
      '<!-- linter-disable-next-n-lines: 01 trailing-spaces -->',
      'd   ',
      'e',
    ].join('\n'));
  });

  it('does not let a later enable cancel a line-scoped disable', () => {
    const text = [
      '<!-- linter-disable-next-n-lines: 3 trailing-spaces -->',
      '<!-- linter-enable trailing-spaces -->',
      'stay   ',
      'also stay   ',
      'trim   ',
    ].join('\n');
    expect(lintTrailing(text)).toBe([
      '<!-- linter-disable-next-n-lines: 3 trailing-spaces -->',
      '<!-- linter-enable trailing-spaces -->',
      'stay   ',
      'also stay   ',
      'trim',
    ].join('\n'));
  });

  it('never modifies a marker line, even when that marker does not disable the running rule', () => {
    const text = [
      '<!-- linter-enable -->   ',
      'a   b',
      '%% linter-disable remove-multiple-spaces %%   ',
      'c   d   ',
      '%% linter-enable %%',
    ].join('\n');

    expect(lintTrailing(text)).toBe([
      '<!-- linter-enable -->   ',
      'a   b',
      '%% linter-disable remove-multiple-spaces %%   ',
      'c   d',
      '%% linter-enable %%',
    ].join('\n'));
    expect(lintMultiple(text)).toBe([
      '<!-- linter-enable -->   ',
      'a b',
      '%% linter-disable remove-multiple-spaces %%   ',
      'c   d   ',
      '%% linter-enable %%',
    ].join('\n'));
  });

  it('ignores markers that are not alone on the line or that sit inside frontmatter, code, inline code, or math', () => {
    expect(lintTrailing([
      'text <!-- linter-disable trailing-spaces -->',
      'trim   ',
    ].join('\n'))).toBe([
      'text <!-- linter-disable trailing-spaces -->',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '---',
      '<!-- linter-disable trailing-spaces -->',
      '---',
      'trim   ',
    ].join('\n'))).toBe([
      '---',
      '<!-- linter-disable trailing-spaces -->',
      '---',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '```',
      '<!-- linter-disable trailing-spaces -->',
      '```',
      'trim   ',
    ].join('\n'))).toBe([
      '```',
      '<!-- linter-disable trailing-spaces -->',
      '```',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      'paragraph',
      '',
      '    <!-- linter-disable trailing-spaces -->',
      'trim   ',
    ].join('\n'))).toBe([
      'paragraph',
      '',
      '    <!-- linter-disable trailing-spaces -->',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '`<!-- linter-disable trailing-spaces -->`',
      'trim   ',
    ].join('\n'))).toBe([
      '`<!-- linter-disable trailing-spaces -->`',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '`code',
      '%% linter-disable trailing-spaces %%',
      'end`',
      'trim   ',
    ].join('\n'))).toBe([
      '`code',
      '%% linter-disable trailing-spaces %%',
      'end`',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '$$',
      '<!-- linter-disable trailing-spaces -->',
      '$$',
      'trim   ',
    ].join('\n'))).toBe([
      '$$',
      '<!-- linter-disable trailing-spaces -->',
      '$$',
      'trim',
    ].join('\n'));

    expect(lintTrailing([
      '   <!-- linter-disable trailing-spaces -->',
      'stay   ',
    ].join('\n'))).toBe([
      '   <!-- linter-disable trailing-spaces -->',
      'stay   ',
    ].join('\n'));
  });

  it('keeps a fake enable inside a code block from closing a real scope', () => {
    const text = [
      '<!-- linter-disable trailing-spaces -->',
      'stay   ',
      '```',
      '<!-- linter-enable -->',
      '```',
      'also stay   ',
      '<!-- linter-enable -->',
      'trim   ',
    ].join('\n');
    expect(lintTrailing(text)).toBe([
      '<!-- linter-disable trailing-spaces -->',
      'stay   ',
      '```',
      '<!-- linter-enable -->',
      '```',
      'also stay   ',
      '<!-- linter-enable -->',
      'trim',
    ].join('\n'));
  });

  it('protects marker lines and all-rule regions from custom regex, but not regions that name other rules', () => {
    const runner = new RulesRunner();
    const replacement = [{
      label: 'collapse spaces',
      find: ' +',
      replace: ' ',
      flags: 'g',
      enabled: true,
    }];

    const disabledForAll = [
      'a  b',
      '<!--  linter-disable  -->',
      'c  d',
      '<!-- linter-enable -->',
      'e  f',
    ].join('\n');
    expect(runner.runCustomRegexReplacement(replacement, disabledForAll)).toBe([
      'a b',
      '<!--  linter-disable  -->',
      'c  d',
      '<!-- linter-enable -->',
      'e f',
    ].join('\n'));

    const disabledForOneRule = [
      '<!-- linter-disable trailing-spaces -->',
      'c  d',
      '<!-- linter-enable -->',
    ].join('\n');
    expect(runner.runCustomRegexReplacement(replacement, disabledForOneRule)).toBe([
      '<!-- linter-disable trailing-spaces -->',
      'c d',
      '<!-- linter-enable -->',
    ].join('\n'));
  });

  it('drops trailing whitespace a rule adds onto a protected placeholder line', () => {
    const text = [
      '<!-- linter-disable -->',
      'stay   ',
      '<!-- linter-enable -->',
    ].join('\n');
    const result = ignoreListOfTypes([IgnoreTypes.customIgnore], text, (value: string) => {
      return value.split('\n').map((line) => line + '  ').join('\n');
    });
    expect(result).toBe(text);
  });

  it('reports per-rule ranges separately from all-rule ranges', () => {
    const text = [
      'a',
      '<!-- linter-disable trailing-spaces -->',
      'b',
      '<!-- linter-enable -->',
      'c',
    ].join('\n');

    const forTrailing = getAllCustomIgnoreSectionsInText(text, 'TRAILING-SPACES');
    const forOther = getAllCustomIgnoreSectionsInText(text, 'remove-multiple-spaces');
    const forEveryRule = getAllCustomIgnoreSectionsInText(text, null);

    const disableAt = text.indexOf('<!-- linter-disable trailing-spaces -->');
    const contentAt = text.indexOf('\nb\n') + 1;
    const enableAt = text.indexOf('<!-- linter-enable -->');
    expect(forTrailing).toEqual([
      {startIndex: disableAt, endIndex: enableAt + '<!-- linter-enable -->'.length},
    ]);
    expect(forOther).toEqual([
      {startIndex: enableAt, endIndex: enableAt + '<!-- linter-enable -->'.length},
      {startIndex: disableAt, endIndex: disableAt + '<!-- linter-disable trailing-spaces -->'.length},
    ]);
    expect(forEveryRule).toEqual(forOther);
    expect(text.slice(contentAt, contentAt + 1)).toBe('b');
  });
});

describe('Scoped ignore examples stay formatted', () => {
  it('matches the documented partial-file example', () => {
    const text = dedent`
      Here is some text
      <!-- linter-disable -->
                                This area will not be formatted
      <!-- linter-enable -->
      More   content goes here...
    `;
    expect(lintMultiple(text)).toBe(dedent`
      Here is some text
      <!-- linter-disable -->
                                This area will not be formatted
      <!-- linter-enable -->
      More content goes here...
    `);
  });
});
