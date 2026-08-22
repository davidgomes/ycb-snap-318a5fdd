import {normalizeRuleAliasList, getIgnoreRangesForRule} from '../src/utils/linter-ignore-markers';
import dedent from 'ts-dedent';

describe('normalizeRuleAliasList', () => {
  const known = new Set(['trailing-spaces', 'consecutive-blank-lines', 'yaml-timestamp']);

  it('normalizes case, drops duplicates, empty entries, and unknown aliases', () => {
    expect(normalizeRuleAliasList('Trailing-Spaces, YAML-TIMESTAMP, trailing-spaces,, unknown-rule,', known))
        .toEqual(['trailing-spaces', 'yaml-timestamp']);
  });

  it('returns an empty list when every entry is unused', () => {
    expect(normalizeRuleAliasList(',,not-a-rule,', known)).toEqual([]);
  });
});

describe('getIgnoreRangesForRule', () => {
  const known = new Set(['trailing-spaces', 'consecutive-blank-lines']);

  it('does not treat markers inside fenced code as active', () => {
    const text = dedent`
      line  ${''}
      \`\`\`
      <!-- linter-disable trailing-spaces -->
      inner  ${''}
      <!-- linter-enable -->
      \`\`\`
      after  ${''}
    `;

    expect(getIgnoreRangesForRule(text, 'trailing-spaces', known)).toEqual([]);
  });

  it('does not treat markers inside YAML as active', () => {
    const text = dedent`
      ---
      key: value
      <!-- linter-disable trailing-spaces -->
      ---
      body  ${''}
    `;

    expect(getIgnoreRangesForRule(text, 'trailing-spaces', known)).toEqual([]);
  });

  it('treats an invalid next-n count as a protected marker with no disable effect', () => {
    const text = dedent`
      <!-- linter-disable-next-n-lines: 0 trailing-spaces -->
      body  ${''}
    `;

    const ranges = getIgnoreRangesForRule(text, 'trailing-spaces', known);
    expect(ranges).toEqual([{startIndex: 0, endIndex: text.indexOf('\n')}]);
  });
});
